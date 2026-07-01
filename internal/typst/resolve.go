package typst

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ── Import syntax ────────────────────────────────────────────────────
//
// Cross-article imports use the `@` prefix inside typst's native
// `#import` / `#include` statements:
//
//   #import "@my-helper-functions"
//   #import "@my-helper-functions": my-func, another-func
//   #include "@shared-footer"
//
// The resolver:
//   1. Scans the source for `#import "@slug"` / `#include "@slug"` patterns
//   2. Looks up each slug in the `articles` table (type='typst')
//   3. Recursively resolves transitive dependencies with cycle detection
//   4. Writes each dependency as `.dep_<article_id>.typ` in the workspace
//      (its own @imports already resolved to .dep paths)
//   5. Rewrites all @slug references in the source to `.dep_<id>.typ`
//      so the native typst CLI resolves them from the workspace directory
//   6. Returns the fully-resolved source, all transitive dependency IDs,
//      and a list of temp dep files for post-compile cleanup.
//
// Non-@ imports (e.g. `#import "template.typ"`) are left untouched so
// system templates and local files continue to work.

// importRe matches both `#import "@slug"` and `#include "@slug"`, capturing
// the slug. Tolerates whitespace between keyword and string; works with
// both `: items` and `as name` suffixes because we only care about the
// quoted path. The regex is intentionally simple — it doesn't skip
// comments (a false match inside `//` or `/* */` produces a clear
// "unknown slug" error the author can fix).
var importRe = regexp.MustCompile(`#(?:import|include)\s+"@([^"]+)"`)

// depFileName returns the workspace-relative basename for a dependency
// file. Using the article ID (integer) guarantees no collisions with
// Unicode/special-character slugs and keeps filenames predictable.
func depFileName(articleID int) string {
	return fmt.Sprintf(".dep_%d.typ", articleID)
}

// articleStub is the minimal article data needed for resolution.
type articleStub struct {
	ID      int
	Slug    string
	Content string
}

// lookupArticle fetches a typst article by slug. Returns nil, nil if not
// found (caller produces a friendly "unknown slug" error).
func lookupArticle(dbx *sql.DB, slug string) (*articleStub, error) {
	var a articleStub
	err := dbx.QueryRow(
		`SELECT id, slug, content FROM articles WHERE type = 'typst' AND slug = ?`,
		slug,
	).Scan(&a.ID, &a.Slug, &a.Content)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// resolveResult holds the output of a dependency-resolution pass.
type resolveResult struct {
	source   string   // rewritten source with @slug → .dep_N.typ paths
	depIDs   []int    // all transitive dependency article IDs (first-seen order)
	depFiles []string // absolute paths of written .dep files (for cleanup)
}

// rewriteImports replaces @slug references in src using the given slug→id map.
func rewriteImports(src string, slugToID map[string]int) string {
	return importRe.ReplaceAllStringFunc(src, func(match string) string {
		sub := importRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		slug := sub[1]
		id, ok := slugToID[slug]
		if !ok {
			return match
		}
		return strings.Replace(match, "@"+slug, depFileName(id), 1)
	})
}

// resolveDependencies parses source, recursively resolves all @-prefixed
// imports from the database, writes dep files into workspaceDir, and
// returns the rewritten source. currentArticleID prevents self-import.
func resolveDependencies(dbx *sql.DB, workspaceDir string, currentArticleID int, source string) (*resolveResult, error) {
	if dbx == nil {
		return &resolveResult{source: source}, nil
	}

	// resolved: id → fully-rewritten source (post-order: children first)
	resolved := make(map[int]string)
	// resolving: set of ids currently on the recursion stack (cycle detect)
	resolving := make(map[int]bool)
	// slugToID: slug → article ID (for rewriting @slug in all sources)
	slugToID := make(map[string]int)

	var depIDOrder []int
	var depFiles []string

	// findSlugs extracts unique @slugs from source in first-seen order.
	findSlugs := func(src string) []string {
		seen := make(map[string]bool)
		var slugs []string
		for _, m := range importRe.FindAllStringSubmatch(src, -1) {
			slug := m[1]
			if !seen[slug] {
				seen[slug] = true
				slugs = append(slugs, slug)
			}
		}
		return slugs
	}

	// resolveOne recursively resolves a single article and its transitive
	// deps, writing the dep file AFTER children are resolved.
	var resolveOne func(a *articleStub, path []int) error
	resolveOne = func(a *articleStub, path []int) error {
		// Cycle detection: a is already on the current recursion path.
		if resolving[a.ID] {
			return fmt.Errorf("typst import cycle detected: %s → @%s",
				formatChain(path, slugToID), a.Slug)
		}
		// Already fully resolved from another branch? Skip.
		if _, ok := resolved[a.ID]; ok {
			return nil
		}

		resolving[a.ID] = true
		slugToID[a.Slug] = a.ID
		defer func() { resolving[a.ID] = false }()

		// Recursively resolve children first (depth-first, post-order).
		for _, slug := range findSlugs(a.Content) {
			dep, err := lookupArticle(dbx, slug)
			if err != nil {
				return fmt.Errorf("lookup @%s: %w", slug, err)
			}
			if dep == nil {
				return fmt.Errorf("typst import references unknown article: @%s (no typst article with slug %q)", slug, slug)
			}
			if dep.ID == currentArticleID {
				return fmt.Errorf("typst article cannot import itself: @%s", slug)
			}
			// Don't re-enter an article that's already resolved.
			if _, ok := resolved[dep.ID]; ok {
				continue
			}
			if err := resolveOne(dep, append(path, a.ID)); err != nil {
				return err
			}
		}

		// Now rewrite this article's source (all children are in slugToID).
		rewritten := rewriteImports(a.Content, slugToID)
		resolved[a.ID] = rewritten

		// Write dep file (never for the current article being compiled).
		if a.ID != currentArticleID {
			depPath := filepath.Join(workspaceDir, depFileName(a.ID))
			if err := os.WriteFile(depPath, []byte(rewritten), 0600); err != nil {
				return fmt.Errorf("write dep file for @%s: %w", a.Slug, err)
			}
			depFiles = append(depFiles, depPath)
			depIDOrder = append(depIDOrder, a.ID)
		}
		return nil
	}

	// Resolve all top-level imports from the main source.
	for _, slug := range findSlugs(source) {
		dep, err := lookupArticle(dbx, slug)
		if err != nil {
			return nil, fmt.Errorf("lookup @%s: %w", slug, err)
		}
		if dep == nil {
			return nil, fmt.Errorf("typst import references unknown article: @%s (no typst article with slug %q)", slug, slug)
		}
		if dep.ID == currentArticleID {
			return nil, fmt.Errorf("typst article cannot import itself: @%s", slug)
		}
		if err := resolveOne(dep, []int{currentArticleID}); err != nil {
			return nil, err
		}
	}

	// Rewrite the main source using the complete slugToID map.
	resolvedSource := rewriteImports(source, slugToID)

	return &resolveResult{
		source:   resolvedSource,
		depIDs:   depIDOrder,
		depFiles: depFiles,
	}, nil
}

// formatChain formats a chain of article IDs for error messages,
// including slug names where known.
func formatChain(ids []int, slugToID map[string]int) string {
	// Build reverse map for the ids we know.
	idToSlug := make(map[int]string)
	for s, id := range slugToID {
		idToSlug[id] = s
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		if slug, ok := idToSlug[id]; ok {
			parts[i] = fmt.Sprintf("@%s", slug)
		} else {
			parts[i] = fmt.Sprintf("article#%d", id)
		}
	}
	return strings.Join(parts, " → ")
}

// formatDepList formats dependency IDs as comma-separated string for
// storage in the typst_cache.dependencies TEXT column.
func formatDepList(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ",")
}
