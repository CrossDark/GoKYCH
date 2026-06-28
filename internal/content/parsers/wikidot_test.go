package parsers

import (
	"strings"
	"testing"
)

// ── Wikidot syntax additions (vs. PyKYCH baseline) ────────────────────
//
// The user's "wikidot测试" article on prod exercised every syntax the
// new parser needed to learn. These tests pin each addition so future
// refactors don't quietly drop support.

func TestWikidotMonoNoLongerEscape(t *testing.T) {
	// `@@...@@` was originally used to HTML-escape a fragment (so the
	// inner < > wouldn't be re-interpreted). Wikidot's actual
	// convention is monospace, so the parser now wraps the inner
	// content in <code>. Authors who genuinely want raw HTML escape
	// should rely on the client-side DOMPurify pass on the rendered
	// output.
	in := `@@这是等宽字体@@`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<code>这是等宽字体</code>`) {
		t.Errorf("expected @@ to wrap in <code>, got %q", out)
	}
}

func TestWikidotInlineColorHashHash(t *testing.T) {
	// `##colorname|text##` — Wikidot's inline-colour shortcut.
	for name, hex := range colorNames {
		in := "##" + name + "|colored text##"
		out := RenderWikidot(in)
		want := `<span style="color:` + hex + `">colored text</span>`
		if !strings.Contains(out, want) {
			t.Errorf("color %q: expected %q in %q", name, want, out)
		}
	}
}

func TestWikidotInlineColorUnknownNamePassthrough(t *testing.T) {
	// Unknown colour name → sanitiser drops the value, the inner
	// text comes through uncoloured.
	in := `##notacolor|plain##`
	out := RenderWikidot(in)
	if strings.Contains(out, `style="color`) {
		t.Errorf("expected no style on unknown colour, got %q", out)
	}
	if !strings.Contains(out, `plain`) {
		t.Errorf("expected text 'plain' to remain, got %q", out)
	}
}

func TestWikidotHeadingLevelsUpToSix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"+ H1", "<h1>H1</h1>"},
		{"++ H2", "<h2>H2</h2>"},
		{"+++ H3", "<h3>H3</h3>"},
		{"++++ H4", "<h4>H4</h4>"},
		{"+++++ H5", "<h5>H5</h5>"},
		{"++++++ H6", "<h6>H6</h6>"},
	}
	for _, c := range cases {
		if out := RenderWikidot(c.in); !strings.Contains(out, c.want) {
			t.Errorf("input %q: expected %q in %q", c.in, c.want, out)
		}
	}
}

func TestWikidotExternalLinkBracketed(t *testing.T) {
	// `[url text]` form.
	in := `[https://example.com Example]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<a href="https://example.com"`) {
		t.Errorf("expected href, got %q", out)
	}
	if !strings.Contains(out, `rel="nofollow noopener"`) {
		t.Errorf("expected rel=nofollow, got %q", out)
	}
	if !strings.Contains(out, `>Example</a>`) {
		t.Errorf("expected display text, got %q", out)
	}
}

func TestWikidotMailtoBracketed(t *testing.T) {
	in := `[mailto:foo@bar.example 发送邮件]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<a href="mailto:foo@bar.example"`) {
		t.Errorf("expected mailto href, got %q", out)
	}
	if !strings.Contains(out, `>发送邮件</a>`) {
		t.Errorf("expected display text, got %q", out)
	}
}

func TestWikidotAutoURL(t *testing.T) {
	in := `见 https://example.com 了解详情`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<a href="https://example.com"`) {
		t.Errorf("expected auto-linked URL, got %q", out)
	}
}

func TestWikidotTableRowSyntax(t *testing.T) {
	in := `||~ Header 1 ||~ Header 2 ||
|| Cell 1-1 || Cell 1-2 ||`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<table class="wiki-table">`) {
		t.Errorf("expected table tag, got %q", out)
	}
	if !strings.Contains(out, `<th>Header 1</th>`) {
		t.Errorf("expected th cell, got %q", out)
	}
	if !strings.Contains(out, `<td>Cell 1-1</td>`) {
		t.Errorf("expected td cell, got %q", out)
	}
}

func TestWikidotTableRowNoParagraphsInCells(t *testing.T) {
	// The bug fixed alongside the table-row addition: cell content
	// was getting wrapped in <p>, which is invalid HTML inside <td>.
	in := `||~ H ||
|| C ||`
	out := RenderWikidot(in)
	if strings.Contains(out, `<td><p>`) || strings.Contains(out, `<th><p>`) {
		t.Errorf("cells should not be wrapped in <p>, got %q", out)
	}
}

func TestWikidotMathBlock(t *testing.T) {
	in := `[[math]]E = mc^2[[/math]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<div class="wikidot-math">\(E = mc^2\)</div>`) {
		t.Errorf("expected math block with paren delimiters, got %q", out)
	}
}

func TestWikidotDivStyle(t *testing.T) {
	in := `[[div style="background: yellow;"]]highlighted[[/div]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<div style="background: yellow;">`) {
		t.Errorf("expected div with style, got %q", out)
	}
}

func TestWikidotSpanStyleNoParagraphInside(t *testing.T) {
	// span is inline; paragraph wrapping inside it is invalid HTML.
	in := `[[span style="background: yellow;"]]highlighted text[[/span]]`
	out := RenderWikidot(in)
	if strings.Contains(out, `<span style="background: yellow;"><p>`) {
		t.Errorf("span should not contain <p>, got %q", out)
	}
}

func TestWikidotYoutubeEmbed(t *testing.T) {
	in := `[[youtube dQw4w9WgXcQ]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"`) {
		t.Errorf("expected youtube iframe, got %q", out)
	}
}

func TestWikidotHtmlBlockSafelyEscaped(t *testing.T) {
	// [[html]]…[[/html]] is shown as escaped <pre>, never rendered.
	in := `[[html]]<iframe src="evil"></iframe>[[/html]]`
	out := RenderWikidot(in)
	if strings.Contains(out, `<iframe`) {
		t.Errorf("[[html]] must not produce a live iframe, got %q", out)
	}
	if !strings.Contains(out, `wikidot-html-escaped`) {
		t.Errorf("expected escaped wrapper class, got %q", out)
	}
	if !strings.Contains(out, `&lt;iframe`) {
		t.Errorf("expected iframe tag to be entity-escaped, got %q", out)
	}
}

func TestWikidotAnchorDefAndJumpLink(t *testing.T) {
	in := `[[a name="目标"]]
这里是内容。
[[/a]]
[#目标 跳过去]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `id="目标"`) {
		t.Errorf("expected anchor def to emit id, got %q", out)
	}
	if !strings.Contains(out, `<a href="#目标"`) {
		t.Errorf("expected jump link to use href, got %q", out)
	}
	if !strings.Contains(out, `>跳过去</a>`) {
		t.Errorf("expected jump link display text, got %q", out)
	}
}

func TestWikidotItalicDoesNotMatchInAutoLinkedURL(t *testing.T) {
	// Regression: the list-rendering phase calls inlineOnly on text
	// that's already been auto-linked, where `https://example.com`
	// contains a `//` that the old italic regex matched, putting
	// `<em>` in the middle of the <a href="https:…">. The fix: the
	// opening `//` must NOT be preceded by `:`.
	in := `* 直接URL：https://example.com`
	out := RenderWikidot(in)
	if strings.Contains(out, `<a href="https:<em>`) {
		t.Errorf("italic leaked into auto-linked URL, got %q", out)
	}
	if !strings.Contains(out, `<a href="https://example.com"`) {
		t.Errorf("expected intact auto-link, got %q", out)
	}
}

func TestWikidotItalicStillMatchesRealItalics(t *testing.T) {
	// Sanity check — the URL-safe restriction didn't break the
	// common case.
	cases := []string{
		`//italic//`,
		`some //italic// text`,
		`行首 //italic// 行尾`,
	}
	for _, in := range cases {
		out := RenderWikidot(in)
		if !strings.Contains(out, `<em>italic</em>`) {
			t.Errorf("input %q: expected <em>italic</em>, got %q", in, out)
		}
	}
}

func TestWikidotFloatLeftRight(t *testing.T) {
	in := `[[float=left]]left content[[/float]]`
	outL := RenderWikidot(in)
	if !strings.Contains(outL, `<div style="float:left">`) {
		t.Errorf("float=left expected, got %q", outL)
	}
	in = `[[float=right]]right content[[/float]]`
	outR := RenderWikidot(in)
	if !strings.Contains(outR, `<div style="float:right">`) {
		t.Errorf("float=right expected, got %q", outR)
	}
}

func TestWikidotImageWithLink(t *testing.T) {
	in := `[[image https://example.com/p.png link="https://example.com/landing"]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<img src="https://example.com/p.png"`) {
		t.Errorf("expected img src, got %q", out)
	}
	if !strings.Contains(out, `<a href="https://example.com/landing">`) {
		t.Errorf("expected wrapping anchor with link, got %q", out)
	}
}
