package typst

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaterializeAssetsCopiesTemplate verifies that init() (and the helper
// it delegates to) actually materializes the embedded template.typ to the
// workspace dir. We exercise the helper directly so the test doesn't
// pollute the project workspace — a temp dir is used and the package-level
// workspaceDir is temporarily swapped in via the env var before the
// helper runs.
//
// Note: the package-level init() runs once at process start and writes to
// the real workspaceDir. The test below uses a fresh temp dir and the
// public helper (not via the env-var — the env var only affects the
// package-level workspaceDir at process start). So we re-implement the
// bits of init() that we want to test: MkdirAll + materializeAssets.
func TestMaterializeAssetsCopiesTemplate(t *testing.T) {
	dir := t.TempDir()
	materializeAssets(dir)

	want := filepath.Join(dir, "template.typ")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("template.typ not materialized to %s: %v", want, err)
	}
	content := string(data)
	if !strings.Contains(content, "template") {
		t.Errorf("template.typ looks empty / wrong: first 100 bytes = %q", content[:min(100, len(content))])
	}
	// Sanity: it should define the public `template` function that users import.
	if !strings.Contains(content, "#let template") {
		t.Errorf("template.typ missing '#let template' definition")
	}
}

// TestMaterializeAssetsRespectsUserEdits verifies that re-running
// materializeAssets does NOT overwrite a user-edited copy. The expected
// behavior is "first-run seeds the file, subsequent runs leave it alone".
func TestMaterializeAssetsRespectsUserEdits(t *testing.T) {
	dir := t.TempDir()
	custom := "// user edit"
	if err := os.WriteFile(filepath.Join(dir, "template.typ"), []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	materializeAssets(dir)
	got, err := os.ReadFile(filepath.Join(dir, "template.typ"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Errorf("user edit was overwritten:\n got: %q\n want: %q", got, custom)
	}
}

// TestCleanupLeakedInputsRemovesTempFiles verifies that .input_*.typ /
// .output_*.html / .output_*.pdf files left behind by a previous crash
// are removed. Also verifies that real user files (template.typ, foo.png)
// are not touched.
func TestCleanupLeakedInputsRemovesTempFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"template.typ":     "// keep me",
		"foo.png":          "fake-png",
		".input_123.typ":   "leak",
		".input_99999.typ": "leak",
		".output_42.html":  "leak",
		".output_42.pdf":   "leak",
		"article-123.typ":  "should NOT be removed (no dot prefix)",
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
	mustExist := []string{"template.typ", "foo.png", "article-123.typ"}
	for _, name := range mustExist {
		if !got[name] {
			t.Errorf("expected %q to survive cleanup, but it was removed", name)
		}
	}
	mustVanish := []string{".input_123.typ", ".input_99999.typ", ".output_42.html", ".output_42.pdf"}
	for _, name := range mustVanish {
		if got[name] {
			t.Errorf("expected %q to be cleaned up, but it survived", name)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestEndToEndImportTemplate is a smoke test: it materializes the
// workspace in a temp dir, then compiles typst source that actually
// imports template.typ. Verifies the workspace dir resolution + asset
// materialization + import path all work together.
//
// Skipped if the typst CLI isn't on the host (CI without typst).
func TestEndToEndImportTemplate(t *testing.T) {
	if !Available() {
		t.Skip("typst CLI not installed; skipping end-to-end test")
	}
	tmp := t.TempDir()
	SetWorkspaceDir(tmp)

	templatePath := filepath.Join(tmp, "template.typ")
	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("template.typ not materialized to %s: %v", templatePath, err)
	}

	src := `#import "template.typ": template, hl
#template[
= Smoke Test
This is body text with #hl[highlighted] word.
]`
	html, err := CompileHTML(src)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(html) < 50 {
		t.Errorf("compiled HTML looks too short (%d bytes): %q", len(html), html)
	}
	fmt.Printf("compilation ok, html=%d bytes\n", len(html))
}

func TestRewriteAssetPaths(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "image with double-quoted /uploads/",
			in:   `#image("/uploads/photo.jpg")`,
			want: `#image("uploads/photo.jpg")`,
		},
		{
			name: "image with single-quoted /avatars/",
			in:   `#image('/avatars/me.png')`,
			want: `#image('avatars/me.png')`,
		},
		{
			name: "image already relative (no leading slash) untouched",
			in:   `#image("uploads/photo.jpg")`,
			want: `#image("uploads/photo.jpg")`,
		},
		{
			name: "non-asset path untouched",
			in:   `#image("template.typ")`,
			want: `#image("template.typ")`,
		},
		{
			name: "external URL untouched",
			in:   `#image("https://example.com/img.png")`,
			want: `#image("https://example.com/img.png")`,
		},
		{
			name: "multiple assets in one source",
			in:   "#image(\"/uploads/a.jpg\")\n#image(\"/avatars/b.png\")",
			want: "#image(\"uploads/a.jpg\")\n#image(\"avatars/b.png\")",
		},
		{
			name: "leading slash in middle of path untouched",
			in:   `#image("sub/path/with/uploads/init.jpg")`,
			want: `#image("sub/path/with/uploads/init.jpg")`,
		},
		{
			name: "#import /uploads/lib.typ",
			in:   `#import "/uploads/my-lib.typ"`,
			want: `#import "uploads/my-lib.typ"`,
		},
		{
			name: "#import /uploads/lib.typ with items",
			in:   `#import "/uploads/my-lib.typ": foo, bar`,
			want: `#import "uploads/my-lib.typ": foo, bar`,
		},
		{
			name: "#include /uploads/header.typ",
			in:   `#include "/uploads/header.typ"`,
			want: `#include "uploads/header.typ"`,
		},
		{
			name: "#read /uploads/data.csv",
			in:   `#read("/uploads/data.csv")`,
			want: `#read("uploads/data.csv")`,
		},
		{
			name: "#bibliography /uploads/refs.bib",
			in:   `#bibliography("/uploads/refs.bib")`,
			want: `#bibliography("uploads/refs.bib")`,
		},
		{
			name: "named argument path: /uploads/",
			in:   `#image(path: "/uploads/logo.png")`,
			want: `#image(path: "uploads/logo.png")`,
		},
		{
			name: "named argument no space after colon",
			in:   `#image(path:"/uploads/logo.png")`,
			want: `#image(path:"uploads/logo.png")`,
		},
		{
			name: "single-quoted #import /avatars/avatar.typ",
			in:   `#import '/avatars/avatar.typ'`,
			want: `#import 'avatars/avatar.typ'`,
		},
		{
			name: "prose string mentioning /uploads/ is NOT rewritten",
			in:   `"See /uploads/help.pdf for details."`,
			want: `"See /uploads/help.pdf for details."`,
		},
		{
			name: "prose with #image and prose mixed",
			in:   `#image("/uploads/a.jpg") + " see /uploads/notes"`,
			want: `#image("uploads/a.jpg") + " see /uploads/notes"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteAssetPaths(tt.in)
			if got != tt.want {
				t.Errorf("rewriteAssetPaths(%q)\n  got:  %q\n  want: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLinkAssetDirs(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	uploads := filepath.Join(tmp, "my-uploads")
	avatars := filepath.Join(tmp, "my-avatars")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(avatars, 0755); err != nil {
		t.Fatal(err)
	}
	// Drop a test file in uploads so we can verify the symlink resolves.
	if err := os.WriteFile(filepath.Join(uploads, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Before SetAssetsDirs is called, dirs are empty → linkAssetDirs is a no-op.
	linkAssetDirs(ws)
	if _, err := os.Stat(filepath.Join(ws, "uploads", "test.txt")); err == nil {
		t.Fatal("expected uploads symlink to not exist yet")
	}

	// Configure dirs and re-link.
	uploadsDir = uploads
	avatarsDir = avatars
	t.Cleanup(func() { uploadsDir = ""; avatarsDir = "" })

	linkAssetDirs(ws)

	// Symlinks should now exist and resolve.
	data, err := os.ReadFile(filepath.Join(ws, "uploads", "test.txt"))
	if err != nil {
		t.Fatalf("reading via uploads symlink: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", data, "hello")
	}
	if fi, err := os.Stat(filepath.Join(ws, "avatars")); err != nil || !fi.IsDir() {
		t.Errorf("avatars symlink not working: err=%v isDir=%v", err, fi != nil && fi.IsDir())
	}

	// Second call should be idempotent (no error, links still work).
	linkAssetDirs(ws)
	if _, err := os.ReadFile(filepath.Join(ws, "uploads", "test.txt")); err != nil {
		t.Fatalf("after second linkAssetDirs: %v", err)
	}
}
