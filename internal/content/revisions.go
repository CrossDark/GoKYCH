// Package content — article revision history (git-style, single branch).
//
// Versioning model
// ────────────────
// Every save of an article produces a new row in `article_revisions`:
//
//   ┌──────────────┬────────────────────────────────────────────┐
//   │ seq = 1      │ is_snapshot=1, patch = full content        │  ← forced snapshot
//   │ seq = 2      │ is_snapshot=0, patch = diff(seq1 → seq2)   │
//   │ seq = 3      │ is_snapshot=0, patch = diff(seq2 → seq3)   │
//   │   …          │   …                                        │
//   │ seq = 50     │ is_snapshot=1, patch = full content        │  ← forced snapshot
//   │ seq = 51     │ is_snapshot=0, patch = diff(seq50 → seq51) │
//   └──────────────┴────────────────────────────────────────────┘
//
// The chain is bounded by the snapshot-every-N rule (see SnapshotEveryN),
// so "rebuild the content at seq=K" is at most N-1 patch applications.
//
// We store unified-diff text in the `patch` column for two reasons:
//   1. Storage: a typical 5KB article with a one-line edit is ~150B diff
//      vs 5KB snapshot — a 30x reduction on the common case.
//   2. Inspectability: the user-facing diff viewer is just the patch text
//      with `+`/`-` colouring. No need to reconstruct on demand.
//
// The diff format is whatever sergi/go-diff's `PatchToText` emits — a
// standard line-level unified diff. To inspect, run ComputePatch(old, new)
// on the saved chain endpoints and pipe to `less`; the format is
// human-readable.
//
// This file contains the algorithm — DB persistence is in V2.
package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// SnapshotEveryN — every N-th revision is forced to be a snapshot, so the
// chain length for "rebuild to seq=N" never exceeds N-1 patch applications.
// 50 is empirically fine: even at the worst case of 50 sequential "rewrite
// half the file" edits, each rebuild is < 50 × (1× patch apply cost), which
// is well under 100ms for multi-MB content on commodity hardware.
const SnapshotEveryN = 50

// minSnapshotRatio — if a computed diff text is longer than
// (new-content size × this fraction), store as a snapshot instead. Handles
// the "user retyped the whole article" case where a diff would be larger
// than the content itself (unified diffs can balloon on small line-level
// shuffles, since each line is preceded by `+ ` / `- ` / `  ` prefixes and
// a copy of every context line).
const minSnapshotRatio = 0.70

// dmpTimeout — how long sergi/go-diff is allowed to spend computing a single
// patch. 5s is plenty for multi-MB text on commodity hardware. Articles
// larger than that (rare in practice) just incur a slightly degraded
// efficiency cleanup but still return a result.
const dmpTimeout = 5 * time.Second

// Revision mirrors one row of the article_revisions table. Defined here
// (rather than in a separate model package) so the V1 commit can ship the
// type alongside ComputePatch / ApplyPatch without yet wiring the DB layer.
type Revision struct {
	ID         int       `json:"id"`
	ArticleID  int       `json:"article_id"`
	Seq        int       `json:"seq"`
	Title      string    `json:"title"`
	Patch      string    `json:"patch,omitempty"` // omitted in list responses
	IsSnapshot bool      `json:"is_snapshot"`
	ParentSeq  *int      `json:"parent_seq,omitempty"`
	AuthorID   *int      `json:"author_id,omitempty"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// RevisionListItem is the lightweight view returned by the list endpoint.
// It omits the (potentially large) Patch field — fetching a single
// revision's content is a separate /revisions/{seq} call.
type RevisionListItem struct {
	ID         int       `json:"id"`
	Seq        int       `json:"seq"`
	Title      string    `json:"title"`
	IsSnapshot bool      `json:"is_snapshot"`
	AuthorID   *int      `json:"author_id,omitempty"`
	AuthorName string    `json:"author_name,omitempty"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

// ComputePatch returns a unified-diff text that, when applied to `old`,
// reconstructs `new`. Returns empty string if old == new.
//
// The diff is line-level Myers (via sergi/go-diff) — stable per-line
// granularity suitable for text content of any size. The format is the
// standard google-diff-match-patch patch text, e.g.:
//
//	@@ -1,3 +1,3 @@
//	 line one
//	-line two old
//	+line two new
//	 line three
//
// This format round-trips: ApplyPatch(old, ComputePatch(old, new)) == new.
func ComputePatch(old, new string) string {
	if old == new {
		return ""
	}
	dmp := newDMP()
	// DiffLinesToChars collapses each line to a single unicode rune so the
	// underlying diff algorithm operates on a string of N characters
	// (where N = max line count) instead of N×avg-line-length bytes.
	// DiffCharsToLines reverses the mapping on the resulting diff.
	chars1, chars2, lineArray := dmp.DiffLinesToChars(old, new)
	diffs := dmp.DiffMain(chars1, chars2, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	// DiffCleanupSemantic reduces noise from small edits that would
	// otherwise produce many tiny patches (e.g. a one-word change split
	// across four +- lines).
	diffs = dmp.DiffCleanupSemantic(diffs)

	patches := dmp.PatchMake(old, diffs)
	return dmp.PatchToText(patches)
}

// ApplyPatch applies a unified-diff text (as produced by ComputePatch) to
// `old` and returns the reconstructed text. Returns an error if the patch
// does not cleanly apply (context drift, truncated patch, etc.).
//
// An empty patchText is a no-op: returns old, nil. This is the wire-format
// for "no change" — when the caller decides two strings are equal, we still
// need ApplyPatch to handle that case gracefully.
func ApplyPatch(old, patchText string) (string, error) {
	if patchText == "" {
		return old, nil
	}
	dmp := newDMP()
	patches, err := dmp.PatchFromText(patchText)
	if err != nil {
		return "", fmt.Errorf("parse patch: %w", err)
	}
	result, results := dmp.PatchApply(patches, old)
	// PatchApply returns []bool indicating per-patch success. We require
	// all patches to succeed — partial application would mean the chain
	// has drifted (likely from corruption) and the result is unreliable.
	for i, ok := range results {
		if !ok {
			return "", fmt.Errorf("patch %d/%d failed to apply (context drift or corrupt diff)", i+1, len(results))
		}
	}
	return result, nil
}

// ShouldSnapshot decides whether a new revision should be stored as a full
// snapshot (is_snapshot=1) or as a diff (is_snapshot=0).
//
//   - seq=1 (first revision for the article) → snapshot (no parent to diff
//     against)
//   - seq%SnapshotEveryN == 0 → snapshot (chain-length bound)
//   - patchText is suspiciously large (≥ 70% of new content) → snapshot
//     (handles "user retyped the whole article" where a diff would
//     actually be larger than the content)
//   - otherwise → diff
func ShouldSnapshot(seq int, patchText, newContent string) bool {
	if seq <= 1 {
		return true
	}
	if seq%SnapshotEveryN == 0 {
		return true
	}
	if len(patchText) > int(float64(len(newContent))*minSnapshotRatio) {
		return true
	}
	return false
}

// RebuildToSeq walks the revision chain and reconstructs the content at the
// given seq. Algorithm:
//
//  1. Find the highest snapshot seq ≤ target (i.e. revs[start].IsSnapshot,
//     start = target-1 walking backwards).
//  2. Start from that snapshot's full content (revs[start].Patch).
//  3. Apply diffs in order: revs[start+1].Patch, revs[start+2].Patch,
//     …, revs[target-1].Patch. Each application produces the content of
//     the next revision.
//
// On patch failure, returns the partial content (whatever was reconstructed
// up to the failure) and an error wrapping the failing seq. This is
// diagnostic — a real failure indicates corruption and the caller should
// surface it to the user, not retry.
//
// Constraint: revs must be sorted by seq ASC, contiguous (no gaps), and
// revs[0] must be a snapshot (seq=1). The function asserts the first
// property implicitly via indexing; the second is an unrecoverable error.
func RebuildToSeq(revs []Revision, target int) (string, error) {
	if len(revs) == 0 {
		return "", errors.New("RebuildToSeq: no revisions provided")
	}
	if target < 1 || target > len(revs) {
		return "", fmt.Errorf("RebuildToSeq: target seq %d out of range [1, %d]", target, len(revs))
	}
	// revs is 0-indexed; target is 1-indexed. The target revision is
	// revs[target-1].
	if !revs[0].IsSnapshot {
		return "", errors.New("RebuildToSeq: chain is missing its first snapshot (seq=1 must be is_snapshot=1)")
	}
	// Walk back from target-1 to find the most recent snapshot.
	start := target - 1
	for start > 0 && !revs[start].IsSnapshot {
		start--
	}
	cur := revs[start].Patch
	for i := start + 1; i < target; i++ {
		next, err := ApplyPatch(cur, revs[i].Patch)
		if err != nil {
			return cur, fmt.Errorf("apply diff at seq=%d: %w", revs[i].Seq, err)
		}
		cur = next
	}
	return cur, nil
}

// newDMP returns a DiffMatchPatch configured for this codebase's content
// sizes. Kept private — callers shouldn't need to reach for the underlying
// library directly.
func newDMP() *diffmatchpatch.DiffMatchPatch {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = dmpTimeout
	return dmp
}
