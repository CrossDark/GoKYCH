package typst

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRewriteImports verifies the import path rewriting logic in isolation
// (no DB, no typst CLI needed).
func TestRewriteImports(t *testing.T) {
	slugToID := map[string]int{
		"helpers": 42,
		"footer":  99,
	}

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "simple import",
			src:  `#import "@helpers"`,
			want: `#import ".dep_42.typ"`,
		},
		{
			name: "import with items",
			src:  `#import "@helpers": my-func, another-func`,
			want: `#import ".dep_42.typ": my-func, another-func`,
		},
		{
			name: "include statement",
			src:  `#include "@footer"`,
			want: `#include ".dep_99.typ"`,
		},
		{
			name: "multiple imports",
			src:  "#import \"@helpers\"\n#include \"@footer\"\n",
			want: "#import \".dep_42.typ\"\n#include \".dep_99.typ\"\n",
		},
		{
			name: "non-@ import untouched",
			src:  `#import "template.typ"`,
			want: `#import "template.typ"`,
		},
		{
			name: "unknown @slug untouched",
			src:  `#import "@nonexistent"`,
			want: `#import "@nonexistent"`,
		},
		{
			name: "as clause",
			src:  `#import "@helpers" as h`,
			want: `#import ".dep_42.typ" as h`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteImports(tt.src, slugToID)
			if got != tt.want {
				t.Errorf("rewriteImports():\n got:  %q\n want: %q", got, tt.want)
			}
		})
	}
}

// TestFindSlugs verifies that importRe correctly extracts @slug references.
func TestFindSlugs(t *testing.T) {
	src := `
#import "template.typ": template
#import "@helpers": my-func
#import "@helpers"
#include "@footer"
#import "@helpers"
`
	matches := importRe.FindAllStringSubmatch(src, -1)
	var slugs []string
	seen := map[string]bool{}
	for _, m := range matches {
		s := m[1]
		if !seen[s] {
			seen[s] = true
			slugs = append(slugs, s)
		}
	}
	if len(slugs) != 2 {
		t.Fatalf("expected 2 unique slugs, got %d: %v", len(slugs), slugs)
	}
	if slugs[0] != "helpers" || slugs[1] != "footer" {
		t.Errorf("unexpected slugs: %v", slugs)
	}
}

// TestDepFileName verifies the dependency file naming convention.
func TestDepFileName(t *testing.T) {
	got := depFileName(42)
	want := ".dep_42.typ"
	if got != want {
		t.Errorf("depFileName(42) = %q, want %q", got, want)
	}
}

// TestFormatDepList verifies comma-separated dependency list formatting.
func TestFormatDepList(t *testing.T) {
	tests := []struct {
		ids  []int
		want string
	}{
		{nil, ""},
		{[]int{}, ""},
		{[]int{1}, "1"},
		{[]int{1, 2, 3}, "1,2,3"},
	}
	for _, tt := range tests {
		got := formatDepList(tt.ids)
		if got != tt.want {
			t.Errorf("formatDepList(%v) = %q, want %q", tt.ids, got, tt.want)
		}
	}
}

// TestCleanupLeakedInputsIncludesDeps verifies that .dep_*.typ files are
// cleaned up alongside .input_ and .output_ files.
func TestCleanupLeakedInputsIncludesDeps(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"template.typ":   "// keep me",
		".input_1.typ":   "leak",
		".output_1.html": "leak",
		".dep_42.typ":    "leak",
		".dep_99.typ":    "leak",
		".input_2.typ":   "leak",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cleanupLeakedInputs(dir)
	entries, _ := os.ReadDir(dir)
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	mustExist := []string{"template.typ"}
	for _, name := range mustExist {
		if !got[name] {
			t.Errorf("expected %q to survive cleanup, but it was removed", name)
		}
	}
	mustVanish := []string{".input_1.typ", ".output_1.html", ".dep_42.typ", ".dep_99.typ", ".input_2.typ"}
	for _, name := range mustVanish {
		if got[name] {
			t.Errorf("expected %q to be cleaned up, but it survived", name)
		}
	}
}

// TestImportReMatchesVariousForms verifies the regex handles common typst
// import/include forms.
func TestImportReMatchesVariousForms(t *testing.T) {
	cases := []struct {
		src  string
		want string // expected captured slug, or "" for no match
	}{
		{`#import "@foo"`, "foo"},
		{`#import "@foo": a, b`, "foo"},
		{`#import "@foo" as f`, "foo"},
		{`#include "@bar"`, "bar"},
		{`  #import   "@baz"`, "baz"}, // whitespace tolerant
		{`#import "template.typ"`, ""},
		{`#import "foo.typ"`, ""},
	}
	for _, c := range cases {
		m := importRe.FindStringSubmatch(c.src)
		var got string
		if m != nil {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("importRe on %q: got slug %q, want %q", c.src, got, c.want)
		}
	}
}

// TestImportReEdgeCases tests edge cases for the import regex.
func TestImportReEdgeCases(t *testing.T) {
	// Slug with hyphens, underscores, dots, Chinese characters, numbers
	cases := []struct {
		src  string
		want string
	}{
		{`#import "@my-helper_v2"`, "my-helper_v2"},
		{`#import "@中文标题"`, "中文标题"},
		{`#import "@math.utils"`, "math.utils"},
		{`#include "@footer-v1.0"`, "footer-v1.0"},
	}
	for _, c := range cases {
		m := importRe.FindStringSubmatch(c.src)
		if m == nil {
			t.Errorf("importRe failed to match: %q", c.src)
			continue
		}
		if m[1] != c.want {
			t.Errorf("importRe on %q: got slug %q, want %q", c.src, m[1], c.want)
		}
	}
}
