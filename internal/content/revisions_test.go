package content

import (
	"strings"
	"testing"
)

// ──────────────────────── ComputePatch ────────────────────────

func TestComputePatch_Identity(t *testing.T) {
	const text = "line1\nline2\nline3\n"
	got := ComputePatch(text, text)
	if got != "" {
		t.Errorf("identity patch should be empty, got %d bytes: %q", len(got), got)
	}
}

func TestComputePatch_EmptyToContent(t *testing.T) {
	const text = "first\nsecond\nthird\n"
	got := ComputePatch("", text)
	if got == "" {
		t.Fatal("empty→content should produce a non-empty patch")
	}
	// roundtrip
	out, err := ApplyPatch("", got)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out != text {
		t.Errorf("roundtrip mismatch:\nwant: %q\ngot:  %q", text, out)
	}
}

func TestComputePatch_ContentToEmpty(t *testing.T) {
	const text = "first\nsecond\nthird\n"
	got := ComputePatch(text, "")
	if got == "" {
		t.Fatal("content→empty should produce a non-empty patch")
	}
	out, err := ApplyPatch(text, got)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out != "" {
		t.Errorf("roundtrip to empty failed: got %q", out)
	}
}

func TestComputePatch_TrailingNewlineMatters(t *testing.T) {
	// Unix text convention: "line\n" vs "line" are different content.
	// The diff should distinguish them.
	got := ComputePatch("hello", "hello\n")
	if got == "" {
		t.Error("adding a trailing newline should be a non-empty diff")
	}
}

// Roundtrip test: take real-looking wikidot/markdown content, apply N
// successive edits, and verify that the cumulative diff chain reconstructs
// the final content exactly.
func TestComputePatch_Roundtrip(t *testing.T) {
	cases := []struct {
		name string
		seq  []string // seq[0] = initial; seq[1..N] = successive edits
	}{
		{
			name: "single append",
			seq: []string{
				"# title\n\npara one.\n",
				"# title\n\npara one.\npara two.\n",
			},
		},
		{
			name: "single line delete",
			seq: []string{
				"a\nb\nc\nd\n",
				"a\nb\nd\n",
			},
		},
		{
			name: "single line modify",
			seq: []string{
				"a\nb\nc\n",
				"a\nB\nc\n",
			},
		},
		{
			name: "many scattered edits",
			seq: []string{
				"L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8\nL9\nL10\n",
				"L1\nL2-modified\nL3\nL4\nL5\nL6\nL7\nL8\nL9\nL10\n",
				"L1\nL2-modified\nL3\nL4\nL5\nL6\nL7\nL8-modified\nL9\nL10\n",
				"L1\nL2-modified\nL3\nL4\nL5\nL6\nL7\nL8-modified\nL9\nL10\nL11-added\n",
			},
		},
		{
			name: "wikidot-style content",
			seq: []string{
				"++++ 标题\n\n[[div]]\n这是段落一。\n[[/div]]\n\n[[div class=\"foo\"]]\n段落二。\n[[/div]]\n",
				"++++ 标题\n\n[[div]]\n这是段落一(修订)。\n[[/div]]\n\n[[div class=\"foo\"]]\n段落二。\n[[/div]]\n",
			},
		},
		{
			name: "unicode chinese",
			seq: []string{
				"第一行\n第二行\n第三行\n",
				"第一行\n第二行(已修改)\n第三行\n第四行(新增)\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := tc.seq[0]
			for i := 1; i < len(tc.seq); i++ {
				patch := ComputePatch(cur, tc.seq[i])
				if patch == "" && tc.seq[i] != cur {
					t.Fatalf("step %d: empty patch for non-identity change", i)
				}
				next, err := ApplyPatch(cur, patch)
				if err != nil {
					t.Fatalf("step %d: apply: %v\npatch:\n%s", i, err, patch)
				}
				if next != tc.seq[i] {
					t.Fatalf("step %d: roundtrip mismatch:\nwant: %q\ngot:  %q", i, tc.seq[i], next)
				}
				cur = next
			}
		})
	}
}

// TestComputePatch_LongChain: simulate the realistic case where a single
// article has 50+ sequential edits. Every step's diff should round-trip
// against the previous step's content; the full chain reconstructs the
// final state exactly.
func TestComputePatch_LongChain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long chain in short mode")
	}
	// Start with 200 lines, mutate one at a time.
	const lineCount = 200
	lines := make([]string, lineCount)
	for i := range lines {
		lines[i] = strings.Repeat("x", i%5+1) + "\n"
	}
	cur := strings.Join(lines, "")
	steps := 60
	for step := 0; step < steps; step++ {
		// Mutate a sliding window: change line 5..7 with step-specific text.
		newLines := strings.Split(cur, "\n")
		newLines[5] = "EDITED step " + itoa(step)
		newLines[6] = "EDITED step " + itoa(step) + " line 2"
		// Occasionally insert a new line.
		if step%10 == 0 {
			newLines = append(newLines[:8], append([]string{"INSERTED at step " + itoa(step)}, newLines[8:]...)...)
		}
		next := strings.Join(newLines, "\n")

		patch := ComputePatch(cur, next)
		if patch == "" {
			t.Fatalf("step %d: empty patch for non-identity change", step)
		}
		got, err := ApplyPatch(cur, patch)
		if err != nil {
			t.Fatalf("step %d: apply: %v\npatch:\n%s", step, err, patch)
		}
		if got != next {
			t.Fatalf("step %d: roundtrip mismatch (%d bytes want, %d bytes got)", step, len(next), len(got))
		}
		cur = next
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ──────────────────────── ApplyPatch ────────────────────────

func TestApplyPatch_EmptyPatch(t *testing.T) {
	const text = "anything\ngoes\nhere\n"
	got, err := ApplyPatch(text, "")
	if err != nil {
		t.Fatalf("empty patch should be a no-op, got error: %v", err)
	}
	if got != text {
		t.Errorf("empty patch must return original text verbatim")
	}
}

func TestApplyPatch_ContextDrift(t *testing.T) {
	// Compute a valid patch, then mutate the "old" before applying —
	// the patch's context lines will not match and ApplyPatch must error.
	const old = "alpha\nbeta\ngamma\ndelta\n"
	const new = "alpha\nBETA\ngamma\ndelta\n"
	patch := ComputePatch(old, new)
	if patch == "" {
		t.Fatal("precondition: patch must be non-empty")
	}

	drifted := "alpha\nDIFFERENT\ngamma\ndelta\n" // line 2 changed
	_, err := ApplyPatch(drifted, patch)
	if err == nil {
		t.Error("applying a patch against drifted context should error, got nil")
	}
}

func TestApplyPatch_GarbagePatch(t *testing.T) {
	// sergi/go-diff's PatchFromText is permissive; pure garbage produces
	// patches that don't match, so we expect an apply error rather than a
	// parse error. Either way: must not silently succeed.
	_, err := ApplyPatch("hello\nworld\n", "@@ -1,1 +1,1 @@\n-this-line-does-not-exist\n+replacement\n")
	if err == nil {
		t.Error("garbage patch should not apply cleanly")
	}
}

// ──────────────────────── ShouldSnapshot ────────────────────────

func TestShouldSnapshot(t *testing.T) {
	cases := []struct {
		name        string
		seq         int
		patchText   string
		newContent  string
		wantSnapshot bool
	}{
		{name: "first revision always snapshot", seq: 1, patchText: "x", newContent: "x", wantSnapshot: true},
		{name: "seq=0 (shouldn't happen) also snapshot", seq: 0, patchText: "x", newContent: "x", wantSnapshot: true},
		{name: "normal diff stays as diff", seq: 2, patchText: "@@ -1 +1 @@\n-old\n+new", newContent: strings.Repeat("x", 1000), wantSnapshot: false},
		{name: "seq=50 forced snapshot", seq: 50, patchText: "@@ -1 +1 @@\n-old\n+new", newContent: strings.Repeat("x", 1000), wantSnapshot: true},
		{name: "seq=100 forced snapshot", seq: 100, patchText: "@@ -1 +1 @@\n-old\n+new", newContent: strings.Repeat("x", 1000), wantSnapshot: true},
		{name: "patch > 70% of new content → snapshot", seq: 7, patchText: strings.Repeat("a", 800), newContent: strings.Repeat("x", 1000), wantSnapshot: true},
		{name: "patch = 70% of new content → still diff (boundary is strict >)", seq: 7, patchText: strings.Repeat("a", 700), newContent: strings.Repeat("x", 1000), wantSnapshot: false},
		{name: "patch much smaller than content → diff", seq: 7, patchText: "@@ -1 +1 @@\n-x\n+y", newContent: strings.Repeat("z", 100000), wantSnapshot: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldSnapshot(tc.seq, tc.patchText, tc.newContent)
			if got != tc.wantSnapshot {
				t.Errorf("seq=%d, patchLen=%d, newLen=%d: want %v, got %v",
					tc.seq, len(tc.patchText), len(tc.newContent), tc.wantSnapshot, got)
			}
		})
	}
}

// ──────────────────────── RebuildToSeq ────────────────────────

// makeChain builds a fake revision chain from a sequence of content
// strings (initial + each edit), using the same rules as production:
//   - rev 0 (seq=1) is always a snapshot
//   - every 50th seq is a snapshot
//   - oversized patches degrade to snapshots
func makeChain(t *testing.T, contents []string) []Revision {
	t.Helper()
	if len(contents) == 0 {
		return nil
	}
	revs := make([]Revision, len(contents))
	for i, c := range contents {
		seq := i + 1
		if i == 0 {
			revs[i] = Revision{Seq: seq, Title: "t", Patch: c, IsSnapshot: true}
			continue
		}
		patch := ComputePatch(contents[i-1], c)
		snap := ShouldSnapshot(seq, patch, c)
		if snap {
			revs[i] = Revision{Seq: seq, Title: "t", Patch: c, IsSnapshot: true}
		} else {
			revs[i] = Revision{Seq: seq, Title: "t", Patch: patch, IsSnapshot: false}
		}
	}
	return revs
}

func TestRebuildToSeq_DirectSnapshot(t *testing.T) {
	chain := makeChain(t, []string{"v1", "v2", "v3"})
	got, err := RebuildToSeq(chain, 1)
	if err != nil {
		t.Fatalf("rebuild seq=1: %v", err)
	}
	if got != "v1" {
		t.Errorf("seq=1: want %q, got %q", "v1", got)
	}
}

func TestRebuildToSeq_FullChain(t *testing.T) {
	contents := []string{
		"line1\nline2\nline3\n",
		"line1\nline2-modified\nline3\n",
		"line1\nline2-modified\nline3\nline4-added\n",
		"line1\nline2-modified\nline3\nline4-added\nline5\n",
	}
	chain := makeChain(t, contents)
	for target := 1; target <= len(contents); target++ {
		got, err := RebuildToSeq(chain, target)
		if err != nil {
			t.Fatalf("rebuild seq=%d: %v", target, err)
		}
		if got != contents[target-1] {
			t.Errorf("seq=%d: roundtrip mismatch\nwant: %q\ngot:  %q", target, contents[target-1], got)
		}
	}
}

func TestRebuildToSeq_AcrossSnapshotBoundary(t *testing.T) {
	// Build a chain that crosses the 50-snapshot boundary: 60 edits.
	// revs[0]  (seq=1)   snapshot
	// revs[1..48]         diffs
	// revs[49] (seq=50)  forced snapshot
	// revs[50..58]        diffs
	// revs[59] (seq=60)  diff
	if testing.Short() {
		t.Skip("skipping 60-step chain in short mode")
	}
	contents := make([]string, 60)
	for i := range contents {
		if i == 0 {
			contents[i] = "init\n"
		} else {
			contents[i] = contents[i-1] + "x" + itoa(i) + "\n"
		}
	}
	chain := makeChain(t, contents)
	// Verify a snapshot landed at seq=50.
	if !chain[49].IsSnapshot {
		t.Fatalf("expected snapshot at seq=50, got is_snapshot=%v", chain[49].IsSnapshot)
	}
	// Rebuild seq=60 — must cross the boundary.
	got, err := RebuildToSeq(chain, 60)
	if err != nil {
		t.Fatalf("rebuild seq=60: %v", err)
	}
	if got != contents[59] {
		t.Errorf("seq=60: roundtrip mismatch (%d bytes want, %d bytes got)", len(contents[59]), len(got))
	}
}

func TestRebuildToSeq_PartialChain(t *testing.T) {
	// Rebuild to seq=3 should only walk revs[0..2].
	contents := []string{
		"a\nb\nc\n",
		"a\nb-modified\nc\n",
		"a\nb-modified\nc\nd-added\n",
		"a\nb-modified\nc\nd-added\ne\n",
	}
	chain := makeChain(t, contents)
	got, err := RebuildToSeq(chain, 3)
	if err != nil {
		t.Fatalf("rebuild seq=3: %v", err)
	}
	if got != contents[2] {
		t.Errorf("seq=3 mismatch:\nwant: %q\ngot:  %q", contents[2], got)
	}
}

func TestRebuildToSeq_OutOfRange(t *testing.T) {
	chain := makeChain(t, []string{"a", "b", "c"})
	if _, err := RebuildToSeq(chain, 0); err == nil {
		t.Error("target=0 should error")
	}
	if _, err := RebuildToSeq(chain, 4); err == nil {
		t.Error("target=4 (out of range) should error")
	}
	if _, err := RebuildToSeq(chain, -1); err == nil {
		t.Error("target=-1 should error")
	}
}

func TestRebuildToSeq_Empty(t *testing.T) {
	if _, err := RebuildToSeq(nil, 1); err == nil {
		t.Error("empty chain should error")
	}
	if _, err := RebuildToSeq([]Revision{}, 1); err == nil {
		t.Error("empty chain (non-nil) should error")
	}
}

func TestRebuildToSeq_FirstMustBeSnapshot(t *testing.T) {
	// revs[0] is not a snapshot — that means the chain is corrupt.
	bad := []Revision{
		{Seq: 1, Patch: "@@ -1 +1 @@\n-a\n+b", IsSnapshot: false},
	}
	if _, err := RebuildToSeq(bad, 1); err == nil {
		t.Error("chain with non-snapshot seq=1 should error")
	}
}

func TestRebuildToSeq_CorruptMiddleDiff(t *testing.T) {
	// seq=1 snapshot, seq=2 valid diff, seq=3 corrupt diff.
	// Use a content size that won't trigger the minSnapshotRatio fallback
	// (otherwise makeChain would store seq=3 as a snapshot and the corruption
	// would be silently trusted by RebuildToSeq's back-walk).
	contents := []string{
		strings.Repeat("a\n", 200),                  // seq=1 snapshot
		strings.Repeat("a\n", 200) + "B\n",          // seq=2 valid diff (small relative to content)
		strings.Repeat("a\n", 200) + "B\nC\n",       // seq=3 — also a diff
	}
	chain := makeChain(t, contents)
	if chain[1].IsSnapshot {
		t.Fatalf("precondition: seq=2 must be a diff, got is_snapshot=true (size ratio fall-back)")
	}
	if chain[2].IsSnapshot {
		t.Fatalf("precondition: seq=3 must be a diff, got is_snapshot=true (size ratio fall-back)")
	}
	// Corrupt seq=3's patch text by replacing the unified-diff header
	// with garbage. PatchFromText is permissive about a lot of things
	// (it'll happily produce a patch with no effect), but a non-`@@`
	// header is rejected at parse time.
	chain[2].Patch = "this is not a unified diff header\n-just garbage\n"

	_, err := RebuildToSeq(chain, 3)
	if err == nil {
		t.Error("corrupt middle diff should produce an error")
	}
	if !strings.Contains(err.Error(), "seq=3") {
		t.Errorf("error should reference the failing seq, got: %v", err)
	}
}

// ──────────────────────── SnapshotEveryN sanity ────────────────────────

func TestSnapshotEveryN_Value(t *testing.T) {
	// Document the constant. If SnapshotEveryN changes, force a human
	// review of the storage/perf trade-off.
	if SnapshotEveryN != 50 {
		t.Errorf("SnapshotEveryN changed to %d — review chain-length and storage trade-off", SnapshotEveryN)
	}
}
