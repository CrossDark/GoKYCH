package parsers

import (
	"strings"
	"testing"
	"time"
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
	// Wikidot headings now carry a stable per-render anchor id
	// (e.g. `id="h1-1"`, `id="h2-3"`) so the `[[toc]]` block
	// can build clickable links to each section. The exact id
	// value depends on render order, so we check the prefix
	// rather than the full string.
	cases := []struct {
		in, wantPrefix string
	}{
		{"+ H1", `<h1 id="h1-`},
		{"++ H2", `<h2 id="h2-`},
		{"+++ H3", `<h3 id="h3-`},
		{"++++ H4", `<h4 id="h4-`},
		{"+++++ H5", `<h5 id="h5-`},
		{"++++++ H6", `<h6 id="h6-`},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		if !strings.Contains(out, c.wantPrefix) {
			t.Errorf("input %q: expected %q in %q", c.in, c.wantPrefix, out)
		}
		// And the matching close tag must be present (not
		// the truncated `</h>` that an earlier draft
		// produced by accident).
		closeTag := ""
		switch c.wantPrefix[:3] {
		case "<h1":
			closeTag = "</h1>"
		case "<h2":
			closeTag = "</h2>"
		case "<h3":
			closeTag = "</h3>"
		case "<h4":
			closeTag = "</h4>"
		case "<h5":
			closeTag = "</h5>"
		case "<h6":
			closeTag = "</h6>"
		}
		if !strings.Contains(out, closeTag) {
			t.Errorf("input %q: missing close tag %q in %q", c.in, closeTag, out)
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
	// The wikidot parser now emits a placeholder div with the
	// video id as a data attribute, instead of a live iframe.
	// The DOMPurify pass on the client forbids iframe, so the
	// post-sanitise step in ArticleView swaps these placeholders
	// for the real iframe (see web/components/ArticleView.tsx).
	// We test the server-side placeholder; the client-side
	// replacement is a separate Playwright check.
	in := `[[youtube dQw4w9WgXcQ]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<div class="wikidot-youtube" data-youtube-id="dQw4w9WgXcQ">`) {
		t.Errorf("expected youtube placeholder div, got %q", out)
	}
	if strings.Contains(out, `<iframe`) {
		t.Errorf("wikidot must not emit a live iframe (DOMPurify strips it client-side), got %q", out)
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

// ── Out-of-scope round (added 2026-06-29) ─────────────────────────────
//
// The six features below (var substitution, footnote interlink,
// [[toc]], [[include]], [[module …]], nested lists) close out the
// 6-item backlog from the original wikidot parser hand-off. Each
// test pins one of the new code paths so future refactors don't
// quietly drop support.

func TestWikidotVarSubstitution(t *testing.T) {
	// `%%name%%` is replaced from the RenderContext.Vars map.
	// Unknown names are left as-is so authors can spot typos.
	ctx := &RenderContext{
		Vars: map[string]string{
			"user_name": "alice",
			"rating":    "4.5",
		},
	}
	out := RenderWikidotCtx(ctx, "Hello, %%user_name%%! Score: %%rating%%")
	if !strings.Contains(out, "Hello, alice!") {
		t.Errorf("expected user_name substituted, got %q", out)
	}
	if !strings.Contains(out, "Score: 4.5") {
		t.Errorf("expected rating substituted, got %q", out)
	}
	// Unknown var is left untouched.
	out2 := RenderWikidotCtx(ctx, "%%not_a_var%% stays")
	if !strings.Contains(out2, "%%not_a_var%%") {
		t.Errorf("expected unknown var to remain, got %q", out2)
	}
	// Without a context (legacy / static), the var syntax
	// passes through unchanged.
	out3 := RenderWikidot("%%user_name%%")
	if !strings.Contains(out3, "%%user_name%%") {
		t.Errorf("expected no-op without context, got %q", out3)
	}
}

func TestWikidotFootnoteInterlink(t *testing.T) {
	// Footnote DEFINITION lines (`^[N] content`) at the bottom
	// of an article are collected; inline `[N]` in the body
	// becomes a back-link to the definition. A `[[footnote]]`
	// list is appended automatically.
	in := `这是正文,带引用[1]和[2]。

**脚注:**
[1] 第一个脚注
[2] 第二个脚注`
	out := RenderWikidot(in)
	// Body refs become <sup><a> back-links to the footer <li>.
	if !strings.Contains(out, `<a href="#fn-1" id="fnref-1">1</a>`) {
		t.Errorf("expected body ref to fn-1, got %q", out)
	}
	if !strings.Contains(out, `<a href="#fn-2" id="fnref-2">2</a>`) {
		t.Errorf("expected body ref to fn-2, got %q", out)
	}
	// Definition list at the end.
	if !strings.Contains(out, `class="footnotes"`) {
		t.Errorf("expected footnote list appended, got %q", out)
	}
	if !strings.Contains(out, `id="fn-1">第一个脚注`) {
		t.Errorf("expected definition line 1, got %q", out)
	}
	if !strings.Contains(out, `id="fn-2">第二个脚注`) {
		t.Errorf("expected definition line 2, got %q", out)
	}
	// The definition lines themselves shouldn't appear as
	// body references.
	if strings.Contains(out, `id="fnref-1">[1] 第一个脚注`) {
		t.Errorf("definition line was rendered as body ref, got %q", out)
	}
	// Backref from the <li> to the body ref id.
	if !strings.Contains(out, `href="#fnref-1"`) {
		t.Errorf("expected backref from <li> to body, got %q", out)
	}
}

func TestWikidotFootnoteUnresolvedRef(t *testing.T) {
	// A `[N]` with no matching definition is left visible
	// (with a distinct class) so the author can see the
	// dangling reference rather than a silent broken anchor.
	in := `见引用[42]但没有定义。`
	out := RenderWikidot(in)
	if !strings.Contains(out, `class="footnote-ref-unresolved"`) {
		t.Errorf("expected unresolved ref marker, got %q", out)
	}
	if strings.Contains(out, `href="#fn-42"`) {
		t.Errorf("unresolved ref must not link, got %q", out)
	}
}

func TestWikidotTOCReplacesMarker(t *testing.T) {
	// The [[toc]] marker is replaced by a <ul> of the article's
	// h2/h3 headings. Heading ids are assigned in render
	// order — the first h3 in the source is "h3-1", then the
	// first h2 is "h2-2", the second h2 is "h2-3", etc. (the
	// h-prefixed sequence counter is global, not per-level).
	in := `++ Section A
text A
+++ Sub A1
++ Section B
[[toc]]`
	out := RenderWikidot(in)
	if strings.Contains(out, "[[toc]]") {
		t.Errorf("[[toc]] marker should be replaced, got %q", out)
	}
	if !strings.Contains(out, `class="wikidot-toc"`) {
		t.Errorf("expected wikidot-toc div, got %q", out)
	}
	if !strings.Contains(out, `href="#h2-2"`) {
		t.Errorf("expected link to h2-2 (Section A), got %q", out)
	}
	if !strings.Contains(out, `href="#h3-1"`) {
		t.Errorf("expected link to h3-1 (Sub A1), got %q", out)
	}
	if !strings.Contains(out, `href="#h2-3"`) {
		t.Errorf("expected link to h2-3 (Section B), got %q", out)
	}
}

func TestWikidotTOCEmpty(t *testing.T) {
	// An article with no h2/h3 should show a "no sections" hint
	// rather than leaving the [[toc]] marker raw.
	in := `Some text but no headings.
[[toc]]`
	out := RenderWikidot(in)
	if strings.Contains(out, "[[toc]]") {
		t.Errorf("[[toc]] should be replaced, got %q", out)
	}
	if !strings.Contains(out, "wikidot-toc-empty") {
		t.Errorf("expected empty toc placeholder, got %q", out)
	}
}

func TestWikidotIncludeMissingSlugFallsThrough(t *testing.T) {
	// Without a PageLookup (or with one that doesn't know the
	// slug), the include marker is left in the source so the
	// author can see it. We don't stash it as a block — the
	// raw text is the right user-facing signal.
	out := RenderWikidotCtx(&RenderContext{}, "[[include nope:page]]")
	if !strings.Contains(out, "[[include nope:page]]") {
		t.Errorf("expected raw include marker when no PageLookup, got %q", out)
	}
}

func TestWikidotIncludeRendersTarget(t *testing.T) {
	// With a PageLookup that returns content for a slug, the
	// include is replaced by the recursively-rendered target
	// HTML. The target's wikidot syntax must be fully expanded
	// (here, a heading).
	lookup := &mockPageLookup{
		includes: map[string]*IncludedPage{
			"wikidot/header": {Type: "wikidot", Content: "++ 子页标题", Title: "header"},
		},
	}
	ctx := &RenderContext{PageLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, "前面。[[include header]]后面。")
	if !strings.Contains(out, `<h2 id="h2-`) {
		t.Errorf("expected included heading rendered, got %q", out)
	}
	if strings.Contains(out, "[[include") {
		t.Errorf("[[include]] marker should be replaced, got %q", out)
	}
}

func TestWikidotIncludeWithVars(t *testing.T) {
	// Include attributes (`[[include slug | name=value]]`) are
	// exposed to the included page as `%%name%%` substitutions.
	// Parent vars still apply unless overridden.
	lookup := &mockPageLookup{
		includes: map[string]*IncludedPage{
			"wikidot/card": {Type: "wikidot", Content: `**%%title%%** by %%author%%`},
		},
	}
	ctx := &RenderContext{
		PageLookup:  lookup,
		ArticleType: "wikidot",
		Vars:        map[string]string{"author": "alice"},
	}
	out := RenderWikidotCtx(ctx, `[[include card |title=Hello]]`)
	if !strings.Contains(out, "<strong>Hello</strong>") {
		t.Errorf("expected title= override to render, got %q", out)
	}
	if !strings.Contains(out, "by alice") {
		t.Errorf("expected author= from parent vars, got %q", out)
	}
}

func TestWikidotModuleListPages(t *testing.T) {
	// [[module ListPages category="*" limit="3" order="created_at desc"]]
	// builds a <ul> of the matching pages, with %%title%% etc.
	// substituted in the inner template. The inner template
	// runs through `inlineOnly`, so basic bold/italic/etc.
	// work — but the template itself doesn't auto-generate
	// links to each page; authors use `%%title%%` as plain
	// text or build links themselves with `[[[slug|%%title%%]]]`.
	lookup := &mockPageLookup{
		entries: []ListPageEntry{
			{Slug: "a", Title: "Page A", AuthorName: "alice", CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
			{Slug: "b", Title: "Page B", AuthorName: "bob", CreatedAt: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)},
		},
	}
	ctx := &RenderContext{PageLookup: lookup, ArticleType: "wikidot"}
	in := `[[module ListPages category="*" limit="5" order="created_at desc"]]* %%title%% (by %%author_name%%)[[/module]]`
	out := RenderWikidotCtx(ctx, in)
	if !strings.Contains(out, "wikidot-module-list") {
		t.Errorf("expected module list wrapper, got %q", out)
	}
	if !strings.Contains(out, "Page A") {
		t.Errorf("expected Page A title, got %q", out)
	}
	if !strings.Contains(out, "Page B") {
		t.Errorf("expected Page B title, got %q", out)
	}
	if !strings.Contains(out, "by alice") {
		t.Errorf("expected author substituted, got %q", out)
	}
	if !strings.Contains(out, "by bob") {
		t.Errorf("expected author substituted, got %q", out)
	}
	// And wikilinks inside the template still get expanded
	// by inlineOnly — so authors can build clickable lists
	// without a separate syntax.
	in2 := `[[module ListPages category="*"]][[[a|Page A]]][[/module]]`
	out2 := RenderWikidotCtx(ctx, in2)
	if !strings.Contains(out2, `href="/wikidot/a"`) {
		t.Errorf("expected wikilink inside template to render, got %q", out2)
	}
}

func TestWikidotModuleRandomPage(t *testing.T) {
	// [[module RandomPage category="*"]]label[[/module]]
	// emits an anchor to one random entry.
	lookup := &mockPageLookup{
		entries: []ListPageEntry{
			{Slug: "x", Title: "Random X"},
		},
	}
	ctx := &RenderContext{PageLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, `[[module RandomPage category="*"]]go[[/module]]`)
	if !strings.Contains(out, `href="/wikidot/x"`) {
		t.Errorf("expected link to /wikidot/x, got %q", out)
	}
	if !strings.Contains(out, `class="wikidot-random-page"`) {
		t.Errorf("expected random-page class, got %q", out)
	}
	if !strings.Contains(out, ">go</a>") {
		t.Errorf("expected label as link text, got %q", out)
	}
}

func TestWikidotModuleWithoutContextFailsGracefully(t *testing.T) {
	// No PageLookup → emit a clear error message rather than
	// silently dropping the construct.
	out := RenderWikidotCtx(nil, `[[module ListPages category="*"]]body[[/module]]`)
	if !strings.Contains(out, "wikidot-module-error") {
		t.Errorf("expected error wrapper, got %q", out)
	}
	if !strings.Contains(out, "PageLookup") {
		t.Errorf("expected error to mention PageLookup, got %q", out)
	}
}

func TestWikidotNestedListUnordered(t *testing.T) {
	// 2-space indent drives the nesting depth. Output should
	// be a proper <ul><ul>…</ul></ul> tree, not a flat list.
	in := `* A
  * A.1
  * A.2
* B`
	out := RenderWikidot(in)
	if !strings.Contains(out, "<ul>") {
		t.Errorf("expected <ul> in output, got %q", out)
	}
	// Count opening <ul> tags. 1 outer + 1 inner for the
	// A.1/A.2 children.
	if got, want := strings.Count(out, "<ul>"), 2; got < want {
		t.Errorf("expected at least %d <ul> openings, got %d in %q", want, got, out)
	}
	// All three top-level items should appear as <li>.
	for _, s := range []string{"A.1", "A.2", "B"} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in nested list output, got %q", s, out)
		}
	}
	// And we shouldn't have the marker-as-tag bug from the
	// pre-rewrite implementation.
	if strings.Contains(out, "<*>") {
		t.Errorf("marker leaked as tag name, got %q", out)
	}
}

func TestWikidotNestedListMixedTypes(t *testing.T) {
	// A `*` list with a `#` child should produce a <ul>
	// containing a nested <ol>.
	in := `* outer
  # child1
  # child2`
	out := RenderWikidot(in)
	if !strings.Contains(out, "<ul>") {
		t.Errorf("expected <ul> for outer, got %q", out)
	}
	if !strings.Contains(out, "<ol>") {
		t.Errorf("expected <ol> for inner, got %q", out)
	}
	if strings.Contains(out, "<*>") || strings.Contains(out, "<#>") {
		t.Errorf("marker leaked as tag name, got %q", out)
	}
}

func TestWikidotNestedDivBalanced(t *testing.T) {
	// The previous regex-based div renderer would close the
	// OUTER `[[/div]]` at the first inner `[[/div]]`, leaving
	// a stray closing tag in the output. Verify that nested
	// divs now render balanced.
	in := `[[div style="border:1px solid #000"]]
外层
[[div style="background:yellow"]]
内层
[[/div]]
[[/div]]`
	out := RenderWikidot(in)
	// Count <div ...> and </div> openers. The divs come back
	// in the rendered HTML (we don't escape them), so the
	// counts should match.
	if got, want := strings.Count(out, "<div"), 2; got != want {
		t.Errorf("expected %d <div openings, got %d in %q", want, got, out)
	}
	if got, want := strings.Count(out, "</div>"), 2; got != want {
		t.Errorf("expected %d </div> closings, got %d in %q", want, got, out)
	}
	if strings.Contains(out, "[[/div]]") {
		t.Errorf("[[/div]] marker should be consumed, got %q", out)
	}
}

func TestWikidotHRShorterThanFourDashes(t *testing.T) {
	// Wikidot accepts `---` (3 dashes) as a horizontal rule;
	// the previous parser required 4. Three or more dashes
	// should now all become <hr>.
	for _, in := range []string{"---", "----", "-----"} {
		out := RenderWikidot(in)
		if !strings.Contains(out, "<hr>") {
			t.Errorf("input %q: expected <hr>, got %q", in, out)
		}
	}
}

func TestWikidotTableColspan(t *testing.T) {
	// `|| ||` and `||||` between cells denote a merge; the
	// previous parser rendered them as empty cells, leaving a
	// visible gap. With colspan the merge is real.
	in := `||~ A ||~ B ||~ C ||
|| 1 |||| 3 ||`
	out := RenderWikidot(in)
	if !strings.Contains(out, `colspan="2"`) {
		t.Errorf("expected colspan=2 for merged cell, got %q", out)
	}
}

func TestWikidotYoutubeIDAcceptsNonStandard(t *testing.T) {
	// Real YouTube ids are 11 chars of [A-Za-z0-9_-], but
	// authors occasionally paste longer strings (e.g. test
	// ids, Chinese descriptions). The parser should accept the
	// broader character class and let the client iframe swap
	// fail gracefully if the id is bogus.
	in := `[[youtube dQw4w9WgXcQ]]
[[youtube notRealIDButShouldStillPlaceholder]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `data-youtube-id="dQw4w9WgXcQ"`) {
		t.Errorf("expected standard id placeholder, got %q", out)
	}
	if !strings.Contains(out, `data-youtube-id="notRealIDButShouldStillPlaceholder"`) {
		t.Errorf("expected non-standard id still placeholdered, got %q", out)
	}
}

// mockPageLookup is a tiny in-memory PageLookup used by the
// include / module tests above. Production adapters wrap the
// MySQL queries against the articles table; this stub keeps
// the parser tests self-contained (no DB needed).
type mockPageLookup struct {
	includes map[string]*IncludedPage
	entries  []ListPageEntry
}

func (m *mockPageLookup) IncludeBySlug(atype, slug string) *IncludedPage {
	if m == nil {
		return nil
	}
	return m.includes[atype+"/"+slug]
}

func (m *mockPageLookup) ListPages(category string, limit int, order string) []ListPageEntry {
	if m == nil {
		return nil
	}
	return m.entries
}

func (m *mockPageLookup) RandomPage(category string) *ListPageEntry {
	if m == nil || len(m.entries) == 0 {
		return nil
	}
	return &m.entries[0]
}
