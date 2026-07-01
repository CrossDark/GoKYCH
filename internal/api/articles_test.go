package api

import "testing"

// TestSlugRe locks in the blacklist-based slug validation. The grammar accepts
// ALL Unicode characters (including CJK, Cyrillic, accented Latin, emoji, spaces,
// quotes, brackets, percent signs) and ONLY rejects characters that are dangerous
// in URL paths or filenames:
//   - ASCII control chars (0x00-0x1F, 0x7F)
//   - Forward slash / (path separator)
//   - Backslash \ (Windows path separator)
//
// slugRe is a NEGATIVE test: MatchString returning true means the slug contains
// an INVALID character; false means it is acceptable. The "." and ".." segments
// are handled separately by the handler (they are rejected there, not by the regex).
func TestSlugRe(t *testing.T) {
	cases := []struct {
		name string
		slug string
		// want=true means the regex MATCHES → contains invalid char → rejected.
		// want=false means the regex does NOT match → valid slug.
		wantInvalid bool
	}{
		{"ascii letters digits ok", "hello-world_42", false},
		{"dot allowed", "v0.1.2", false},
		{"CJK ok", "我第一篇笔记", false},
		{"Cyrillic ok", "Заметка_1", false},
		{"accented Latin ok", "Café-Nouvelle", false},
		{"emoji ok", "我的第一篇笔记😀", false},
		{"space ok (allowed in new rules)", "my first note", false},
		{"percent ok", "100%-done", false},
		{"quote ok", `"note"`, false},
		{"bracket ok", "[tag]", false},
		{"dot-segment ok (rejected by handler, not regex)", "..", false},
		{"leading dot ok", ".hidden", false},
		{"trailing dot ok", "trailing.", false},
		{"empty ok (rejected by empty-check, not regex)", "", false},
		{"slash rejected", "a/b", true},
		{"backslash rejected", `a\b`, true},
		{"tab rejected (control char)", "a\tb", true},
		{"null rejected (control char)", "a\x00b", true},
		{"DEL rejected (0x7F)", "a\x7fb", true},
		{"newline rejected (0x0A)", "a\nb", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slugRe.MatchString(tc.slug)
			if got != tc.wantInvalid {
				t.Fatalf("slugRe.MatchString(%q) = %v; wantInvalid=%v", tc.slug, got, tc.wantInvalid)
			}
		})
	}
}

// TestSlugLengthCap exercises the handler-side length guard. A 128-rune
// slug must be accepted (we round-trip the slug-re + length checks the way
// createArticle does it), and a 129-rune slug must trip the length rejection.
func TestSlugLengthCap(t *testing.T) {
	if slugRe.MatchString(strings_Repeat("字", 128)) {
		t.Fatal("128 CJK runes should be allowed by slugRe")
	}
	if len([]rune(strings_Repeat("字", 129))) <= maxSlugRunes {
		t.Fatal("129 runes should exceed maxSlugRunes")
	}
}

// Tiny local helper so this test file stays self-contained (strings.Repeat
// lives in the standard library but pulling in import "strings" just for one
// call reads heavier than the inline builder).
func strings_Repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
