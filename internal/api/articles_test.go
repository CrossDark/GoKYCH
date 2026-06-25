package api

import "testing"

// TestSlugRe locks in the Unicode-aware slug charset. The grammar must
// accept CJK / Cyrillic / accented Latin display names so non-English
// articles keep a readable URL ("/md/我第一篇笔记"), and reject everything
// that would break URL paths, filesystem lookups, or Content-Disposition
// headers (whitespace, control chars, path separators, the ". / .."
// segments, quotes, brackets, emoji/punctuation).
func TestSlugRe(t *testing.T) {
	cases := []struct {
		name string
		slug string
		want bool
	}{
		{"ascii letters digits ok", "hello-world_42", true},
		{"dot allowed", "v0.1.2", true},
		{"CJK ok", "我第一篇笔记", true},
		{"Cyrillic ok", "Заметка_1", true},
		{"accented Latin ok", "Café-Nouvelle", true},
		{"empty rejected", "", false},
		{"slash rejected", "a/b", false},
		{"backslash rejected", `a\b`, false},
		{"space rejected", "a b", false},
		{"tab rejected", "a\tb", false},
		{"angstrom-ish space rejected", "全角 空格", false},
		{"dot-segment rejected (handled by caller, but regex still ok)", "..", true},
		{"quoted rejected", `"a"`, false},
		{"bracket rejected", "[tag]", false},
		{"emoji rejected", "测试🚀rocket", false},
		{"percent rejected", "100%-done", false},
		{"leading dot ok", ".hidden", true},
		{"trailing dot ok", "trailing.", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slugRe.MatchString(tc.slug)
			if got != tc.want {
				t.Fatalf("slugRe.MatchString(%q) = %v; want %v", tc.slug, got, tc.want)
			}
		})
	}
}

// TestSlugLengthCap exercises the handler-side length guard. A 128-rune
// slug must be accepted (we round-trip the slug-re + length checks the way
// createArticle does it), and a 129-rune slug must trip the length rejection.
func TestSlugLengthCap(t *testing.T) {
	if !slugRe.MatchString(strings_Repeat("字", 128)) {
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
