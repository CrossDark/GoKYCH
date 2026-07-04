package parsers

import (
	"testing"
)

// `contains` is shared with sanitize_test.go (declared there).

// TestRenderBBCodeSizeKeywords verifies the keyword table: `small`,
// `large`, etc. resolve to rem values via sizeMap.
func TestRenderBBCodeSizeKeywords(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`[size=small]x[/size]`, `font-size:0.8rem`},
		{`[size=large]x[/size]`, `font-size:1.25rem`},
		{`[size=medium]x[/size]`, `font-size:1rem`},
		{`[size=SMALL]x[/size]`, `font-size:0.8rem`}, // case-insensitive
	}
	for _, c := range cases {
		got := RenderBBCode(c.in)
		if !contains(got, c.want) {
			t.Errorf("RenderBBCode(%q) = %q, want contains %q", c.in, got, c.want)
		}
		if !contains(got, `>x</span>`) {
			t.Errorf("RenderBBCode(%q) = %q, want inner text wrapped", c.in, got)
		}
	}
}

// TestRenderBBCodeSizeScale123to7 verifies the classic HTML 1-7 font
// scale: each digit maps to a rem value via sizeScaleMap. Without
// this branch `[size=7]` used to emit `font-size:7` (unitless, the
// browser silently ignores it — looks "broken").
func TestRenderBBCodeSizeScale123to7(t *testing.T) {
	cases := map[string]string{
		"1": "0.5rem",
		"2": "0.75rem",
		"3": "1rem",
		"4": "1.25rem",
		"5": "1.5rem",
		"6": "1.75rem",
		"7": "2rem",
	}
	for n, want := range cases {
		in := "[size=" + n + "]x[/size]"
		got := RenderBBCode(in)
		if !contains(got, "font-size:"+want) {
			t.Errorf("RenderBBCode(%q) = %q, want contains %q", in, got, "font-size:"+want)
		}
	}
}

// TestRenderBBCodeSizeBareNumberPx is the regression test for the
// original bug the user reported: `[size=10]`, `[size=14]`, `[size=20]`,
// `[size=30]` were all rendering as `font-size:N` (unitless → invisible).
// Now they get an implicit `px` because that's the most common reading
// for bare numbers > 7 and ≤ 40.
func TestRenderBBCodeSizeBareNumberPx(t *testing.T) {
	cases := []string{"10", "14", "20", "30", "12", "24"}
	for _, n := range cases {
		in := "[size=" + n + "]x[/size]"
		want := "font-size:" + n + "px"
		got := RenderBBCode(in)
		if !contains(got, want) {
			t.Errorf("RenderBBCode(%q) = %q, want contains %q", in, got, want)
		}
		if contains(got, "font-size:"+n+`"`) {
			// Guard against the old bug slipping back: a bare unitless
			// `font-size:14"` would mean we forgot the px.
			t.Errorf("RenderBBCode(%q) leaked unitless font-size", in)
		}
	}
}

// TestRenderBBCodeSizeBareNumberPercent verifies the phpBB convention:
// bare numbers > 40 are treated as percentages. `[size=150]` should
// render at 150% (the canonical "big text" example from the user).
func TestRenderBBCodeSizeBareNumberPercent(t *testing.T) {
	cases := []string{"50", "80", "100", "150", "200"}
	for _, n := range cases {
		in := "[size=" + n + "]x[/size]"
		want := "font-size:" + n + "%"
		got := RenderBBCode(in)
		if !contains(got, want) {
			t.Errorf("RenderBBCode(%q) = %q, want contains %q", in, got, want)
		}
	}
}

// TestRenderBBCodeSizeExplicitUnit verifies that explicit-unit values
// (e.g. `0.8em`, `12pt`, `24px`) are passed through verbatim after
// sanitizeCSSValue validation.
func TestRenderBBCodeSizeExplicitUnit(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`[size=0.8em]x[/size]`, `font-size:0.8em`},
		{`[size=1.5em]x[/size]`, `font-size:1.5em`},
		{`[size=12pt]x[/size]`, `font-size:12pt`},
		{`[size=24px]x[/size]`, `font-size:24px`},
		{`[size=80%]x[/size]`, `font-size:80%`},
	}
	for _, c := range cases {
		got := RenderBBCode(c.in)
		if !contains(got, c.want) {
			t.Errorf("RenderBBCode(%q) = %q, want contains %q", c.in, got, c.want)
		}
	}
}

// TestRenderBBCodeSizeNested verifies that a `[size=14]…[size=20]…[/size]…[/size]`
// produces two nested span wrappers (not a torn-up mess where the
// inner opener leaks out).
func TestRenderBBCodeSizeNested(t *testing.T) {
	in := `[size=14]normal [size=20]bigger[/size] normal[/size]`
	want := `<span style="font-size:14px">normal <span style="font-size:20px">bigger</span> normal</span>`
	got := RenderBBCode(in)
	if got != want {
		t.Errorf("RenderBBCode(%q) = %q, want %q", in, got, want)
	}
	if contains(got, "[size=") {
		t.Errorf("RenderBBCode(%q) leaked unprocessed opener", in)
	}
}

// TestRenderBBCodeSizeInvalidDropsWrapper verifies that an unrecognised
// or sanitiser-rejected size value drops the whole wrapper and re-emits
// only the inner body — same as Wikidot's `[[size bad]]x[[/size]]`
// fallback. Prevents the live XSS payload
// `[size=12px; background:url(javascript:alert(1))]x[/size]` from
// ever rendering as a span with a live `background:url(...)` style.
func TestRenderBBCodeSizeInvalidDropsWrapper(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// CSS injection via semicolon-stuffed declaration list.
		{`[size=12px; background:url(javascript:alert(1))]x[/size]`, `x`},
		// Unknown keyword.
		{`[size=giant]x[/size]`, `x`},
		// Empty value.
		{`[size=]x[/size]`, `x`},
		// Blocked by sanitiser (expression).
		{`[size=expression(alert(1))]x[/size]`, `x`},
	}
	for _, c := range cases {
		got := RenderBBCode(c.in)
		if contains(got, "<span") {
			t.Errorf("RenderBBCode(%q) = %q, want no span wrapper", c.in, got)
		}
		if contains(got, "javascript:") || contains(got, "expression(") {
			t.Errorf("RenderBBCode(%q) = %q, want XSS payload stripped", c.in, got)
		}
		if !contains(got, c.want) {
			t.Errorf("RenderBBCode(%q) = %q, want inner text %q preserved", c.in, got, c.want)
		}
	}
}

// TestRenderBBCodeSizeUnmatchedClose verifies that a `[size=14]x` with
// no `[/size]` leaves the opener raw (the author can see their typo)
// instead of swallowing text up to the next `[/size]`.
func TestRenderBBCodeSizeUnmatchedClose(t *testing.T) {
	in := `[size=14]x no close here`
	got := RenderBBCode(in)
	if !contains(got, "[size=14]x no close here") {
		t.Errorf("RenderBBCode(%q) = %q, want raw preserved", in, got)
	}
}

// TestRenderBBCodeHeadings verifies [h1]-[h6] are rendered as proper
// heading tags. Inline formatting inside the heading must still run
// (so `[h1][b]x[/b][/h1]` produces `<h1><strong>x</strong></h1>`),
// matching the Go backend's heading pass ordering.
func TestRenderBBCodeHeadings(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`[h1]一级标题 H1[/h1]`, `<h1>一级标题 H1</h1>`},
		{`[h2]二级标题 H2[/h2]`, `<h2>二级标题 H2</h2>`},
		{`[h3]三级标题 H3[/h3]`, `<h3>三级标题 H3</h3>`},
		{`[h4]H4[/h4]`, `<h4>H4</h4>`},
		{`[h5]H5[/h5]`, `<h5>H5</h5>`},
		{`[h6]H6[/h6]`, `<h6>H6</h6>`},
		// Inline inside heading — heading pass runs before inline so
		// `[b]` inside is still processed.
		{`[h1][b]Bold H1[/b][/h1]`, `<h1><strong>Bold H1</strong></h1>`},
	}
	for _, c := range cases {
		got := RenderBBCode(c.in)
		if got != c.want {
			t.Errorf("RenderBBCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRenderBBCodeHeadingWithSize verifies that a `[size=…]` block
// inside a heading still wraps the inner text in a font-size span,
// because the heading pass runs before the size pass.
func TestRenderBBCodeHeadingWithSize(t *testing.T) {
	in := `[h1][size=14]小字标题[/size][/h1]`
	got := RenderBBCode(in)
	want := `<h1><span style="font-size:14px">小字标题</span></h1>`
	if got != want {
		t.Errorf("RenderBBCode(%q) = %q, want %q", in, got, want)
	}
}