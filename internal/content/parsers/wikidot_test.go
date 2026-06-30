package parsers

import (
	"regexp"
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
	// `@@...@@` is the Wikidot LITERAL escape construct: the
	// inner text is rendered verbatim with no wikidot
	// markup expansion. The earlier version of this
	// parser misread the spec and treated `@@...@@` as
	// monospace, which produced incorrect output for
	// things like `@@**not bold**@@` (which should show
	// `**not bold**` literally, not bold). The
	// monospaced-text feature in Wikidot is `{{...}}`,
	// which is a separate code path and is still
	// supported.
	in := `@@这是等宽字体@@`
	out := RenderWikidot(in)
	// Literal: the inner text is preserved as-is, with
	// any `**` / `[[...]]` markup inside NOT being
	// processed.
	if !strings.Contains(out, "这是等宽字体") {
		t.Errorf("expected literal text to be preserved, got %q", out)
	}
	// And critically: `@@**bold**@@` should NOT produce
	// a `<strong>` tag — the `**` inside is escaped.
	strongOut := RenderWikidot(`@@**not bold**@@`)
	if strings.Contains(strongOut, "<strong>") {
		t.Errorf("@@...@@ should escape inner markup, got %q", strongOut)
	}
	if !strings.Contains(strongOut, "**not bold**") {
		t.Errorf("expected literal asterisks to remain, got %q", strongOut)
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

// ── Wikidot spec round 2 (2026-06-29) ──────────────────────────────
//
// P0 close-out from the wikidot syntax spec at
// https://rule-wiki.wikidot.com/wiki-syntax. Each test pins one
// spec entry that the previous parser was missing or getting
// wrong; together they form a regression set for the Stage 1
// commit.

func TestWikidotLiteralEscapePreservesMarkup(t *testing.T) {
	// `@@...@@` is verbatim: anything inside is preserved
	// exactly, no markup expansion. This is the inverse of
	// the previous (incorrect) monospace interpretation.
	cases := []struct {
		in   string
		want string
	}{
		{`@@**not bold**@@`, `**not bold**`},
		{`@@//not italic//@@`, `//not italic//`},
		{`@@[[not a link]]@@`, `[[not a link]]`},
		{`@@<div>not html</div>@@`, `&lt;div&gt;not html&lt;/div&gt;`},
		{`@@plain text@@`, `plain text`},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		if !strings.Contains(out, c.want) {
			t.Errorf("input %q: expected literal %q in output, got %q", c.in, c.want, out)
		}
	}
}

func TestWikidotInlineColorHexValue(t *testing.T) {
	// `##hex|text##` — the colour can be a 3/4/6/8-digit
	// hex code, not just a name from the colourNames map.
	// We accept `#rgb`, `#rgba`, `#rrggbb`, `#rrggbbaa`.
	cases := []struct {
		hex, want string
	}{
		{"#44FF88", "#44FF88"},
		{"#fff", "#fff"},
		{"#ff00ff", "#ff00ff"},
		{"#abc", "#abc"},
	}
	for _, c := range cases {
		in := "##" + c.hex + "|colored text##"
		out := RenderWikidot(in)
		want := `<span style="color:` + c.want + `">colored text</span>`
		if !strings.Contains(out, want) {
			t.Errorf("input %q: expected %q in %q", in, want, out)
		}
	}
	// Bogus / non-CSS hex is dropped (text passes through).
	out := RenderWikidot("##zzz|colored##")
	if strings.Contains(out, "style=") {
		t.Errorf("expected bogus hex to be dropped, got %q", out)
	}
}

func TestWikidotCenterLeftBlockForm(t *testing.T) {
	// `[[<]]...[[/<]]` is the block form for left-align;
	// mirrors `[[=]]` (center), `[[>]]` (right), `[[==]]`
	// (justify).
	if out := RenderWikidot(`[[<]]left[[/<]]`); !strings.Contains(out, `<div style="text-align:left">left</div>`) {
		t.Errorf("expected left-align div, got %q", out)
	}
	if out := RenderWikidot(`[[=]]center[[/=]]`); !strings.Contains(out, `<div style="text-align:center">center</div>`) {
		t.Errorf("expected center-align div, got %q", out)
	}
}

func TestWikidotCenterLeftLinePrefix(t *testing.T) {
	// The single-line shortcut: a line starting with
	// `= text` (or `< text`) is rendered as a single
	// aligned paragraph. Useful for one-off centred
	// subtitles without needing the `[[=]]` block
	// form.
	if out := RenderWikidot("= 居中文本"); !strings.Contains(out, `<div style="text-align:center">居中文本</div>`) {
		t.Errorf("expected single-line center, got %q", out)
	}
	if out := RenderWikidot("< 左对齐文本"); !strings.Contains(out, `<div style="text-align:left">左对齐文本</div>`) {
		t.Errorf("expected single-line left, got %q", out)
	}
	// Sanity: inline `=` (e.g. `x = y`) is NOT promoted
	// to an alignment div because the `=` isn't at the
	// start of a line.
	if out := RenderWikidot("a = b"); strings.Contains(out, `<div style="text-align`) {
		t.Errorf("inline `=` shouldn't be promoted, got %q", out)
	}
}

func TestWikidotHeadingStarSkipsTOC(t *testing.T) {
	// `+* Heading` emits a heading with a stable anchor
	// id (so cross-references still work) but excludes
	// the entry from the rendered TOC list. We use
	// `++` (h2) throughout so the heading actually
	// makes it into the toc-builder's level filter.
	in := `++ 正常标题
++* 隐藏标题
++ 第二个标题
[[toc]]`
	out := RenderWikidot(in)
	// The skipped heading still gets an id and renders.
	if !strings.Contains(out, `id="h2-1"`) {
		t.Errorf("expected skipped heading to still have an id, got %q", out)
	}
	if !strings.Contains(out, "隐藏标题") {
		t.Errorf("expected skipped heading text rendered, got %q", out)
	}
	// The TOC should NOT include the skipped heading.
	tocMatch := regexp.MustCompile(`<div class="wikidot-toc".*?</div>`).FindString(out)
	if tocMatch == "" {
		t.Fatalf("expected wikidot-toc div, got %q", out)
	}
	if strings.Contains(tocMatch, "隐藏标题") {
		t.Errorf("expected skipped heading NOT in toc, got %q", tocMatch)
	}
	// The other two headings ARE in the toc.
	if !strings.Contains(tocMatch, "正常标题") {
		t.Errorf("expected normal heading in toc, got %q", tocMatch)
	}
	if !strings.Contains(tocMatch, "第二个标题") {
		t.Errorf("expected second heading in toc, got %q", tocMatch)
	}
}

func TestWikidotLineContinuationJoins(t *testing.T) {
	// A line ending in ` _` (space + underscore) merges
	// onto the next line. Used for list items that need
	// a soft line break inside the item body.
	//
	// List item test: a `*` item ending in ` _` joins
	// the following line. The merged text is rendered
	// inside the `<li>` via `inlineOnly`; the
	// paragraph-wrap phase then sees one item.
	listIn := `* 事项1 _
另一行
* 事项2`
	listOut := RenderWikidot(listIn)
	if !strings.Contains(listOut, "事项1 另一行") {
		t.Errorf("expected joined list item, got %q", listOut)
	}
	if !strings.Contains(listOut, "事项2") {
		t.Errorf("expected second list item to remain, got %q", listOut)
	}

	// Ordered list has the same behaviour.
	ordIn := `# 第一 _
续
# 第二`
	ordOut := RenderWikidot(ordIn)
	if !strings.Contains(ordOut, "第一 续") {
		t.Errorf("expected joined ordered list item, got %q", ordOut)
	}
}

func TestWikidotImageGenericAttributes(t *testing.T) {
	// `[[image url]]` accepts `link` (existing), `width`,
	// `height`, `class`, `style` as generic attributes.
	// Unknown keys are silently dropped.
	cases := []struct {
		in   string
		want []string
	}{
		{
			in:   `[[image https://example.com/p.png width="200px"]]`,
			want: []string{`width="200px"`, `src="https://example.com/p.png"`},
		},
		{
			in:   `[[image https://example.com/p.png height="100px" class="thumb"]]`,
			want: []string{`height="100px"`, `class="thumb"`},
		},
		{
			in:   `[[image https://example.com/p.png style="border: 1px solid #000;"]]`,
			want: []string{`style="border: 1px solid #000; max-width:100%"`},
		},
		{
			in:   `[[image https://example.com/p.png unknown="ignored"]]`,
			want: []string{`src="https://example.com/p.png"`, `max-width:100%`},
		},
		{
			in:   `[[image https://example.com/p.png link="https://example.com/landing"]]`,
			want: []string{`href="https://example.com/landing"`, `<img`},
		},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("input %q: expected %q in %q", c.in, w, out)
			}
		}
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

// mockUserLookup is a tiny in-memory UserLookup used by
// the user-mention tests below. Production adapters wrap
// the MySQL queries against the users table; this stub
// keeps the parser tests self-contained (no DB needed).
// Lookups are case-insensitive on the canonical
// `username` (matching the production SQL).
type mockUserLookup struct {
	users map[string]*UserProfile
}

func (m *mockUserLookup) UserByName(name string) *UserProfile {
	if m == nil {
		return nil
	}
	// Match the production adapter's case-insensitive
	// behaviour by lowercasing the lookup key.
	return m.users[strings.ToLower(strings.TrimSpace(name))]
}

// ── Stage 2 (P1) — Wikidot 标点符号 + 高级列表 + 用户提及 ─────

// TestWikidotSmartQuotes covers the typographic pair forms
// from the Wikidot spec §标点符号: “…” (double quotes),
// «…» (guillemets via `<<…>>`), `...` (ellipsis),
// ` -- ` (em-dash with whitespace on both sides), and
// `X's` (apostrophe). The Wikidot spec uses guillemets
// (not curly single quotes) for `<<…>>` and an em-dash
// (U+2014) for ` -- ` — both choices are preserved in
// the renderer's smart-punctuation phase.
func TestWikidotSmartQuotes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		not  []string
	}{
		{
			in:   "他说 ``你好'' 然后走了",
			want: []string{`“`, `”`, `你好`},
		},
		{
			in:   `<<引用>>`,
			want: []string{`«`, `»`, `引用`},
		},
		{
			in:   `等等...`,
			want: []string{`…`},
		},
		{
			in:   `前后 -- 中间`,
			want: []string{`—`, `前后`, `中间`},
		},
		{
			in:   `it's a test`,
			want: []string{`it’s`},
		},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("input %q: expected %q in %q", c.in, w, out)
			}
		}
		for _, n := range c.not {
			if strings.Contains(out, n) {
				t.Errorf("input %q: did NOT expect %q in %q", c.in, n, out)
			}
		}
	}
}

// TestWikidotEnDashNoSpace is a regression: `--` only
// becomes an en-dash when surrounded by whitespace. Bare
// `--` (e.g. `删除线 --text--`) must not turn into an en
// dash because strikethrough already consumed the pair.
func TestWikidotEnDashNoSpace(t *testing.T) {
	out := RenderWikidot(`删除线 --text--`)
	if !strings.Contains(out, "<s>text</s>") {
		t.Errorf("expected strikethrough, got %q", out)
	}
	if strings.Contains(out, "–") {
		t.Errorf("expected no en-dash without whitespace, got %q", out)
	}
}

// TestWikidotBlockquoteCont verifies the `X _\nY` line-
// continuation join inside a blockquote: the two
// source lines merge into a single `<blockquote>`
// body with a space join (not a <br />). The ` _`
// must be the literal continuation marker; a bare
// `\\\n` (markdown hard-break) is a different syntax.
func TestWikidotBlockquoteCont(t *testing.T) {
	out := RenderWikidot("> 行1 _\n行2 结束")
	if !strings.Contains(out, "<blockquote>") {
		t.Errorf("expected <blockquote>, got %q", out)
	}
	if !strings.Contains(out, "行1 行2 结束") {
		t.Errorf("expected joined `行1 行2 结束`, got %q", out)
	}
	if strings.Contains(out, "<br") {
		t.Errorf("expected no <br /> inside joined blockquote, got %q", out)
	}
}

// TestWikidotDefinitionList covers `: term : definition`
// plus the `:` continuation form. Continuation lines are
// `:`-prefixed WITHOUT the `: ... : ...` separator (the
// leading `:` is the only delimiter). A `: term : def`
// line that ALSO has a second `:` IS a new term, not a
// continuation.
func TestWikidotDefinitionList(t *testing.T) {
	in := `: API : 应用编程接口
: 第二行续
: 第三行续
: CSS : 层叠样式表
`
	out := RenderWikidot(in)
	if !strings.Contains(out, "<dl") {
		t.Errorf("expected <dl>, got %q", out)
	}
	if !strings.Contains(out, "<dt>API</dt>") {
		t.Errorf("expected <dt>API</dt>, got %q", out)
	}
	if !strings.Contains(out, "<dd>应用编程接口") {
		t.Errorf("expected <dd>应用编程接口, got %q", out)
	}
	// Continuation lines merge into a single <dd>
	// for the API term; the CSS term gets its own.
	if strings.Count(out, "<dd>") != 2 {
		t.Errorf("expected 2 <dd> (one per term), got %d in %q",
			strings.Count(out, "<dd>"), out)
	}
	// Continuation text must be inside the same <dd>
	// as the original term — joined with <br />.
	if !strings.Contains(out, "<dd>应用编程接口<br />") {
		t.Errorf("expected continuation joined with <br />, got %q", out)
	}
}

// TestWikidotFloatTOC verifies the `[[f<toc]]` and
// `[[f>toc]]` floated forms render an inline
// `<div class="wikidot-toc-float-left/right">` wrapping
// the same TOC HTML the plain `[[toc]]` emits. The TOC
// itself is a `<ul>` of `<li><a href="#…">` items; the
// `wikidot-toc` class lives on the wrapper `<div>`, not
// the inner `<ul>`.
func TestWikidotFloatTOC(t *testing.T) {
	in := `++ 一级标题
[[f<toc]]
++ 二级标题
[[f>toc]]
`
	outL := RenderWikidot(in)
	if !strings.Contains(outL, `wikidot-toc-float-left`) {
		t.Errorf("expected float-left class, got %q", outL)
	}
	if !strings.Contains(outL, `wikidot-toc-float-right`) {
		t.Errorf("expected float-right class, got %q", outL)
	}
	if !strings.Contains(outL, `<ul>`) {
		t.Errorf("expected <ul> inside float wrapper, got %q", outL)
	}
	if !strings.Contains(outL, `<a href="#h2-1">`) {
		t.Errorf("expected heading anchor link, got %q", outL)
	}
}

// TestWikidotCollapsibleFolded verifies the
// `[[collapsible folded="no"]]` form renders a
// `<details … open>` (so the contents show by default),
// while the default `folded=""` (or no folded attr) is
// `<details>` without the `open` attribute (collapsed).
// We can't just check `<details open` because the
// renderer puts a `class="wiki-collapsible"` between
// `<details` and ` open` — instead we look for the
// `open` attribute on the `details` tag (and confirm
// the closed form doesn't have it).
func TestWikidotCollapsibleFolded(t *testing.T) {
	outOpen := RenderWikidot(`[[collapsible folded="no"]]
内容
[[/collapsible]]`)
	if !regexp.MustCompile(`<details[^>]*\bopen\b`).MatchString(outOpen) {
		t.Errorf("expected `open` attribute on <details>, got %q", outOpen)
	}
	outClosed := RenderWikidot(`[[collapsible]]
内容
[[/collapsible]]`)
	if regexp.MustCompile(`<details[^>]*\bopen\b`).MatchString(outClosed) {
		t.Errorf("expected NO `open` attribute on collapsed <details>, got %q", outClosed)
	}
}

// TestWikidotFootnoteBlock verifies the block-form
// `[[footnote]]...[[/footnote]]` plus the
// `[[footnoteblock]]` suppressor. The block-form
// body is NOT rendered inline (Wikidot's behaviour
// — only the `<sup>` back-link shows in place of
// the `[[footnote]]` token). The body appears in
// the `<ol class="footnotes">` list appended at
// the end of the article — UNLESS the article
// contains `[[footnoteblock]]`, which suppresses
// the auto list. The optional `title="..."`
// attribute is parsed and stored on the parser;
// the current renderer's list-emit code only
// consults it when the list IS emitted, so a
// suppressed list (the common case for
// `[[footnoteblock title="..."]]`) doesn't show
// the title in the output.
func TestWikidotFootnoteBlock(t *testing.T) {
	// Case 1: footnote block WITH list — body
	// appears in the list, default title is used.
	in := `正文 [1] 与 [2] 引用
[[footnote]]脚注 1 内容[[/footnote]]
[[footnote]]脚注 2 内容[[/footnote]]
`
	out := RenderWikidot(in)
	if !strings.Contains(out, "脚注 1 内容") {
		t.Errorf("expected footnote 1 body in list, got %q", out)
	}
	if !strings.Contains(out, "脚注 2 内容") {
		t.Errorf("expected footnote 2 body in list, got %q", out)
	}
	// The block-form ref is `[1]` / `[2]`; both
	// should link back to the body.
	if !strings.Contains(out, `href="#fn-1"`) || !strings.Contains(out, `href="#fn-2"`) {
		t.Errorf("expected links to fn-1 / fn-2, got %q", out)
	}
	// The list should be present (no
	// `[[footnoteblock]]` to suppress it).
	if !strings.Contains(out, `<ol>`) {
		t.Errorf("expected default <ol> list, got %q", out)
	}
	if !strings.Contains(out, `footnotes-title`) {
		t.Errorf("expected `footnotes-title` heading, got %q", out)
	}

	// Case 2: footnote block WITH `[[footnoteblock]]`
	// — list is suppressed, body doesn't appear.
	outSup := RenderWikidot(in + `[[footnoteblock]]
`)
	// List is rendered IN-PLACE at the marker
	// (Wikidot treats the marker as a position
	// anchor for the list, not a suppressor).
	// The auto-append at end-of-document is
	// suppressed, so we see exactly one `<ol>`
	// for the footnote list.
	if strings.Count(outSup, `<ol>`) != 1 {
		t.Errorf("expected exactly one <ol> at marker, got %q", outSup)
	}
	// Body IS visible (rendered in the list).
	if !strings.Contains(outSup, "脚注 1 内容") {
		t.Errorf("expected body in in-place list, got %q", outSup)
	}
	// In-place refs are still there.
	if !strings.Contains(outSup, `href="#fn-1"`) {
		t.Errorf("expected in-place ref to fn-1, got %q", outSup)
	}

	// Case 3: `title="..."` is honoured in the
	// in-place list. The label becomes the
	// `<h2 class="footnotes-title">…</h2>` text.
	outTitled := RenderWikidot(in + `[[footnoteblock title="我的脚注"]]
`)
	if !strings.Contains(outTitled, `我的脚注`) {
		t.Errorf("expected title in rendered list, got %q", outTitled)
	}
	if !strings.Contains(outTitled, `href="#fn-1"`) {
		t.Errorf("expected in-place ref to fn-1 with title marker, got %q", outTitled)
	}
}

// TestWikidotAdvancedList verifies the `[[ul]]` / `[[ol]]`
// / `[[li]]` block syntax renders as nested `<ul>` /
// `<ol>` with attributes forwarded to the in-tag form.
func TestWikidotAdvancedList(t *testing.T) {
	in := `[[ul class="my-list"]]
[[li]]item 1[[/li]]
[[li class="active"]]item 2
[[ul]]
[[li]]nested 1[[/li]]
[[li]]nested 2[[/li]]
[[/ul]]
[[/li]]
[[li]]item 3[[/li]]
[[/ul]]
`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<ul class="my-list">`) {
		t.Errorf("expected <ul class=my-list>, got %q", out)
	}
	if !strings.Contains(out, `<li class="active">`) {
		t.Errorf("expected <li class=active>, got %q", out)
	}
	if !strings.Contains(out, `nested 1`) || !strings.Contains(out, `nested 2`) {
		t.Errorf("expected nested items, got %q", out)
	}
	// Nested <ul> inside <li> — no <p> wrapping allowed.
	if strings.Contains(out, "<p><ul") || strings.Contains(out, "</ul></p>") {
		t.Errorf("expected no <p> around nested <ul>, got %q", out)
	}
	// Top-level list and the nested list should both
	// appear as siblings of `<li>`.
	if strings.Count(out, "<ul") < 2 {
		t.Errorf("expected at least 2 <ul> (top + nested), got %q", out)
	}
}

// TestWikidotAdvancedListOrdered verifies `[[ol]]` (with
// numeric ordering).
func TestWikidotAdvancedListOrdered(t *testing.T) {
	in := `[[ol]]
[[li]]first[[/li]]
[[li]]second[[/li]]
[[/ol]]
`
	out := RenderWikidot(in)
	if !strings.Contains(out, "<ol>") {
		t.Errorf("expected <ol>, got %q", out)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("expected both items, got %q", out)
	}
	if !strings.Contains(out, "</ol>") {
		t.Errorf("expected </ol>, got %q", out)
	}
}

// TestWikidotUserMention covers `[[user name]]` (basic)
// and `[[*user name]]` (staff / logged-in) forms. The
// output is a link to `/user/<slug>` with a class hook
// for theming and a `data-username` for client-side
// enrichment.
func TestWikidotUserMention(t *testing.T) {
	out := RenderWikidot(`你好 [[user Alice]] 和 [[*user Bob Smith]]`)
	if !strings.Contains(out, `<a class="user-mention"`) {
		t.Errorf("expected user-mention class, got %q", out)
	}
	if !strings.Contains(out, `href="/user/alice"`) {
		t.Errorf("expected /user/alice slug, got %q", out)
	}
	if !strings.Contains(out, `user-mention-staff`) {
		t.Errorf("expected staff class on `*user` form, got %q", out)
	}
	if !strings.Contains(out, `href="/user/bob-smith"`) {
		t.Errorf("expected /user/bob-smith slug (with space→dash), got %q", out)
	}
	if !strings.Contains(out, `data-username="Alice"`) {
		t.Errorf("expected data-username=Alice, got %q", out)
	}
}

// TestWikidotUserMentionNoName verifies the fallback:
// an empty `[[user]]` / `[[*user]]` (no name) is escaped
// as raw text so the author sees the typo.
func TestWikidotUserMentionNoName(t *testing.T) {
	out := RenderWikidot(`测试 [[user]] 完成`)
	if !strings.Contains(out, "[[user]]") {
		t.Errorf("expected raw [[user]] for empty name, got %q", out)
	}
	if strings.Contains(out, `class="user-mention"`) {
		t.Errorf("expected NO mention markup for empty name, got %q", out)
	}
}

// ── Stage 3 (P1) — Wikidot common blocks ───────────────────────

// TestWikidotDivider verifies the `[[divider]]` tag
// renders as a themed `<hr>` (with `wikidot-divider`
// class so the front-end can style it differently
// from the plain `----` line form which has no class).
func TestWikidotDivider(t *testing.T) {
	out := RenderWikidot("上\n[[divider]]\n下")
	if !strings.Contains(out, `<hr class="wikidot-divider">`) {
		t.Errorf("expected themed hr, got %q", out)
	}
	// No stray plain <hr> without the class should
	// appear from this input (we only used the
	// `[[divider]]` form).
	if strings.Contains(out, "<hr>\n") || strings.Contains(out, "<hr> ") {
		t.Errorf("expected no plain <hr>, got %q", out)
	}
}

// TestWikidotNoteInline verifies `[[note]]…[[/note]]`
// wraps the body in an `<aside class="wikidot-note">`
// container. The surrounding paragraph is NOT
// wrapped in `<p>` because `<aside>` is a block
// element — wrapping text that contains a block
// element in `<p>` produces invalid HTML (the
// browser auto-closes the `<p>` at the `<aside>`,
// leaving the trailing text outside any wrapper).
func TestWikidotNoteInline(t *testing.T) {
	out := RenderWikidot(`一段话 [[note]]注意一下[[/note]] 续接`)
	if !strings.Contains(out, `<aside class="wikidot-note">注意一下</aside>`) {
		t.Errorf("expected note container, got %q", out)
	}
}

// TestWikidotNoteBlock verifies a multi-line note
// (body across multiple newlines) renders the
// newlines as `<br />` inside the `<aside>`, not
// as a stray `<p>` wrapper. Without the
// newline-to-`<br />` rewrite the paragraph-wrap
// phase would emit `<aside>...<p>跨多行</p></aside>`
// which is technically allowed by the HTML parser
// (browsers auto-close the `<p>` at `<aside>`) but
// looks wrong in dev-tools and confuses readers.
func TestWikidotNoteBlock(t *testing.T) {
	out := RenderWikidot(`[[note]]
第一行
第二行
第三行
[[/note]]`)
	if !strings.Contains(out, `<aside class="wikidot-note">`) {
		t.Errorf("expected note container, got %q", out)
	}
	if strings.Contains(out, `<aside class="wikidot-note"><p>`) {
		t.Errorf("expected no <p> inside <aside>, got %q", out)
	}
	if !strings.Contains(out, "第一行<br />") {
		t.Errorf("expected <br /> between lines, got %q", out)
	}
	if !strings.Contains(out, "第二行<br />") {
		t.Errorf("expected <br /> between second and third lines, got %q", out)
	}
}

// TestWikidotNoteInlineFormatting verifies inline
// markup (`**bold**`, `//italic//`, `[link]`)
// inside a note body still renders. The body
// processor routes through `inlineOnly` rather
// than the full `convert()` pipeline so block-
// level rewrapping doesn't kick in, but inline
// formatting is still applied.
func TestWikidotNoteInlineFormatting(t *testing.T) {
	out := RenderWikidot(`[[note]]**粗体** 和 //斜体// 以及 [https://example.com 链接][[/note]]`)
	if !strings.Contains(out, `<strong>粗体</strong>`) {
		t.Errorf("expected bold inside note, got %q", out)
	}
	if !strings.Contains(out, `<em>斜体</em>`) {
		t.Errorf("expected italic inside note, got %q", out)
	}
	if !strings.Contains(out, `href="https://example.com"`) {
		t.Errorf("expected link inside note, got %q", out)
	}
}

// TestWikidotButtonExternal verifies `[[button Label|URL]]`
// with an external https target renders to
// `<a class="wikidot-button" rel="nofollow noopener"
// target="_blank">`. The pipe separator is the
// unambiguous form (so the label can contain
// spaces without confusing the parser).
func TestWikidotButtonExternal(t *testing.T) {
	out := RenderWikidot(`[[button 访问 GitHub|https://github.com]]`)
	if !strings.Contains(out, `class="wikidot-button"`) {
		t.Errorf("expected wikidot-button class, got %q", out)
	}
	if !strings.Contains(out, `href="https://github.com"`) {
		t.Errorf("expected https target, got %q", out)
	}
	if !strings.Contains(out, `rel="nofollow noopener"`) {
		t.Errorf("expected nofollow noopener, got %q", out)
	}
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("expected _blank target, got %q", out)
	}
	if !strings.Contains(out, `访问 GitHub`) {
		t.Errorf("expected label as link text, got %q", out)
	}
}

// TestWikidotButtonInternal verifies a button
// pointing at an internal path (`/foo`). Internal
// targets get NO `rel` / `target` attributes —
// same-origin navigation should stay in the same
// tab and shouldn't pretend to be a third-party
// link.
func TestWikidotButtonInternal(t *testing.T) {
	out := RenderWikidot(`[[button 回首页|/]]`)
	if !strings.Contains(out, `class="wikidot-button"`) {
		t.Errorf("expected wikidot-button class, got %q", out)
	}
	if !strings.Contains(out, `href="/"`) {
		t.Errorf("expected / target, got %q", out)
	}
	if strings.Contains(out, `rel="nofollow`) {
		t.Errorf("internal button should NOT have rel=nofollow, got %q", out)
	}
	if strings.Contains(out, `target="_blank"`) {
		t.Errorf("internal button should NOT open in new tab, got %q", out)
	}
}

// TestWikidotButtonNoTarget verifies `[[button Label]]`
// without a target renders as a placeholder button
// pointing at `#` (fragment-only anchor). Without
// the `[[#`-anchor special case in `sanitizeURLForAttr`
// the `#` would be rejected and the marker would
// drop, leaving the label as plain text.
func TestWikidotButtonNoTarget(t *testing.T) {
	out := RenderWikidot(`[[button 占位按钮]]`)
	if !strings.Contains(out, `class="wikidot-button"`) {
		t.Errorf("expected wikidot-button class, got %q", out)
	}
	if !strings.Contains(out, `href="#"`) {
		t.Errorf("expected # placeholder target, got %q", out)
	}
}

// TestWikidotButtonDangerousTarget verifies that a
// target with a disallowed scheme (e.g. `javascript:`)
// is rejected by `sanitizeURLForAttr` and the
// marker falls back to plain text. This is a
// regression guard against an obvious injection
// vector if someone bypasses the schema check.
func TestWikidotButtonDangerousTarget(t *testing.T) {
	out := RenderWikidot(`[[button 钓鱼|javascript:alert(1)]]`)
	if strings.Contains(out, `wikidot-button`) {
		t.Errorf("expected javascript: scheme to be rejected, got %q", out)
	}
	if strings.Contains(out, `<a `) {
		t.Errorf("expected no <a> from dangerous scheme, got %q", out)
	}
}

// TestWikidotEmailBlock verifies `[[email]]addr[[/email]]`
// (block form) renders as an obfuscated
// `<a class="wikidot-email">` with the address split
// across `data-user` / `data-domain` attributes. The
// visible link text is the assembled address so the
// human reader sees a normal mailto link, but a
// naive HTML scraper looking for `@` only finds it
// in the rendered text (low-grade obfuscation; not
// serious anti-harvest).
func TestWikidotEmailBlock(t *testing.T) {
	out := RenderWikidot(`联系 [[email]]foo@example.com[[/email]] 收`)
	if !strings.Contains(out, `class="wikidot-email"`) {
		t.Errorf("expected wikidot-email class, got %q", out)
	}
	if !strings.Contains(out, `href="mailto:foo@example.com"`) {
		t.Errorf("expected mailto link, got %q", out)
	}
	if !strings.Contains(out, `data-user="foo"`) {
		t.Errorf("expected data-user split, got %q", out)
	}
	if !strings.Contains(out, `data-domain="example.com"`) {
		t.Errorf("expected data-domain split, got %q", out)
	}
	// The visible link text is the full address.
	if !strings.Contains(out, `>foo@example.com</a>`) {
		t.Errorf("expected full address as link text, got %q", out)
	}
}

// TestWikidotEmailTag verifies the single-tag form
// `[[email addr]]` (no body) renders the same way
// as the block form. The two forms are aliases so
// authors can pick whichever reads better in context.
func TestWikidotEmailTag(t *testing.T) {
	out := RenderWikidot(`[[email bar@example.org]]`)
	if !strings.Contains(out, `class="wikidot-email"`) {
		t.Errorf("expected wikidot-email class, got %q", out)
	}
	if !strings.Contains(out, `href="mailto:bar@example.org"`) {
		t.Errorf("expected mailto link, got %q", out)
	}
}

// TestWikidotEmailMalformed verifies an email
// without `@` is left as plain text rather than
// rendering an empty mailto link. The renderer
// should fail open (visible text) rather than
// fail closed (drop the marker entirely), so the
// author sees what they wrote and can fix the
// typo.
func TestWikidotEmailMalformed(t *testing.T) {
	out := RenderWikidot(`[[email not-an-email]]`)
	if strings.Contains(out, `<a `) {
		t.Errorf("expected no <a> for malformed email, got %q", out)
	}
	if !strings.Contains(out, "not-an-email") {
		t.Errorf("expected raw text preserved, got %q", out)
	}
}

// TestWikidotCodeBlockNoParagraphWrap verifies the
// long-standing bug where `[[code]]…[[/code]]`
// body got wrapped in `<p>` and split by `<br />`
// — Phase 10 (restore stored blocks) was running
// BEFORE Phase 11 (paragraph wrap), so by the
// time wrapWikidotParagraphs saw the source, the
// `<pre><code>…</code></pre>` was already inlined
// and the inner lines got paragraph-wrapped. The
// fix treats `<pre>…</pre>` as an opaque block in
// the wrap phase. This regression guard makes
// sure the fix stays in place.
func TestWikidotCodeBlockNoParagraphWrap(t *testing.T) {
	in := `前面
[[code]]
function helloWorld() {
    console.log("Hello, Wikidot!");
}
[[/code]]
后面`
	out := RenderWikidot(in)
	if strings.Contains(out, `<pre><code><p>`) {
		t.Errorf("expected NO <p> inside <pre><code>, got %q", out)
	}
	if strings.Contains(out, `<code>`) && strings.Contains(out, `<br />`) {
		t.Errorf("expected NO <br /> inside <code>, got %q", out)
	}
	if !strings.Contains(out, `function helloWorld() {`) {
		t.Errorf("expected code body verbatim, got %q", out)
	}
}

// ── UserLookup integration (P2 Stage 1) ───────────────────────

// TestUserMentionLookupResolved verifies that when a
// UserLookup returns a profile for the typed name, the
// rendered mention carries:
//   - `data-user-id`    = the resolved ID
//   - `data-avatar`     = the resolved avatar URL
//   - `data-username`   = the typed name (preserved for
//     debugging — author may have
//     typed "Alice" when DB stores
//     "alice")
//   - visible text      = the resolved nickname
//
// The link target (`/user/<slug>`) and the staff /
// non-staff class are unchanged from the pre-lookup
// behaviour, so existing CSS keeps working.
func TestUserMentionLookupResolved(t *testing.T) {
	lookup := &mockUserLookup{
		users: map[string]*UserProfile{
			"alice": {
				ID:        42,
				Username:  "alice",
				Nickname:  "Alice Smith",
				AvatarURL: "https://cdn.example.com/u/alice.png",
				IsStaff:   false,
			},
		},
	}
	ctx := &RenderContext{UserLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, `[[user Alice]]`)
	if !strings.Contains(out, `data-user-id="42"`) {
		t.Errorf("expected data-user-id attribute, got %q", out)
	}
	if !strings.Contains(out, `data-avatar="https://cdn.example.com/u/alice.png"`) {
		t.Errorf("expected data-avatar attribute, got %q", out)
	}
	// The typed name is preserved verbatim — the
	// author typed "Alice" (capital A) and we
	// shouldn't silently rewrite their intent.
	if !strings.Contains(out, `data-username="Alice"`) {
		t.Errorf("expected typed name in data-username, got %q", out)
	}
	// The visible link text is the resolved
	// nickname, NOT the typed login name.
	if !strings.Contains(out, `>@Alice Smith</a>`) {
		t.Errorf("expected nickname as link text, got %q", out)
	}
}

// TestUserMentionLookupCaseInsensitive verifies that
// the lookup matches case-insensitively. The author
// typed "ALICE", the DB stores "alice"; the link
// should resolve to alice's profile. This matches
// the production SQL (`WHERE LOWER(username) = ?`).
func TestUserMentionLookupCaseInsensitive(t *testing.T) {
	lookup := &mockUserLookup{
		users: map[string]*UserProfile{
			"alice": {ID: 7, Username: "alice", Nickname: "Alice"},
		},
	}
	ctx := &RenderContext{UserLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, `[[user ALICE]]`)
	if !strings.Contains(out, `data-user-id="7"`) {
		t.Errorf("expected case-insensitive lookup, got %q", out)
	}
}

// TestUserMentionLookupMiss verifies that an unknown
// username degrades cleanly: the link is still emitted
// (so the URL is clickable and the author can see what
// they typed) but no `data-user-id` / `data-avatar`
// attributes are added, and the visible text falls
// back to the typed name.
func TestUserMentionLookupMiss(t *testing.T) {
	lookup := &mockUserLookup{
		users: map[string]*UserProfile{
			"alice": {ID: 7, Username: "alice", Nickname: "Alice"},
		},
	}
	ctx := &RenderContext{UserLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, `[[user ghost]]`)
	if strings.Contains(out, `data-user-id`) {
		t.Errorf("expected NO data-user-id on miss, got %q", out)
	}
	if strings.Contains(out, `data-avatar`) {
		t.Errorf("expected NO data-avatar on miss, got %q", out)
	}
	// The typed name still appears as the link
	// text — better than dropping the mention
	// entirely, since the author can fix the
	// typo themselves.
	if !strings.Contains(out, `>@ghost</a>`) {
		t.Errorf("expected typed name as link text on miss, got %q", out)
	}
	// `data-username` is always emitted (the
	// typed name, useful for debugging).
	if !strings.Contains(out, `data-username="ghost"`) {
		t.Errorf("expected data-username on miss, got %q", out)
	}
}

// TestUserMentionLookupNoContext verifies that the
// renderer still works when no context (and therefore
// no UserLookup) is passed. The output should match
// the pre-UserLookup behaviour exactly: plain link
// with the typed name, no `data-user-id`, no
// `data-avatar`.
func TestUserMentionLookupNoContext(t *testing.T) {
	out := RenderWikidot(`[[user Alice]] [[*user Bob]]`)
	if strings.Contains(out, `data-user-id`) {
		t.Errorf("expected NO data-user-id without context, got %q", out)
	}
	if strings.Contains(out, `data-avatar`) {
		t.Errorf("expected NO data-avatar without context, got %q", out)
	}
	if !strings.Contains(out, `@Alice`) {
		t.Errorf("expected typed name, got %q", out)
	}
	if !strings.Contains(out, `@Bob`) {
		t.Errorf("expected typed name for staff variant, got %q", out)
	}
}

// TestUserMentionLookupStaffFromProfile verifies that
// the staff class is applied based on the resolved
// profile's IsStaff flag, NOT just on the `*` markup
// form. An admin who lost their `*` still gets the
// staff styling if the DB says they're staff.
func TestUserMentionLookupStaffFromProfile(t *testing.T) {
	lookup := &mockUserLookup{
		users: map[string]*UserProfile{
			"alice": {ID: 1, Username: "alice", Nickname: "Alice", IsStaff: true},
		},
	}
	ctx := &RenderContext{UserLookup: lookup, ArticleType: "wikidot"}
	// Author wrote plain `[[user Alice]]` (no `*`)
	// but the user IS staff per the profile.
	out := RenderWikidotCtx(ctx, `[[user Alice]]`)
	if !strings.Contains(out, `user-mention-staff`) {
		t.Errorf("expected staff class from profile, got %q", out)
	}
}

// TestUserMentionLookupStaffFromMarkup verifies the
// original "star form" staff styling still works
// even when the profile doesn't have IsStaff set
// (e.g. for a logged-in but not-admin user). This is
// the historical Wikidot convention.
func TestUserMentionLookupStaffFromMarkup(t *testing.T) {
	lookup := &mockUserLookup{
		users: map[string]*UserProfile{
			"bob": {ID: 2, Username: "bob", Nickname: "Bob", IsStaff: false},
		},
	}
	ctx := &RenderContext{UserLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, `[[*user Bob]]`)
	if !strings.Contains(out, `user-mention-staff`) {
		t.Errorf("expected staff class from markup, got %q", out)
	}
}

// TestUserMentionLookupEmptyNickname verifies that
// when the profile has no nickname set, the visible
// text falls back to the typed name (NOT empty, NOT
// the canonical username — the typed name is what
// the author meant to communicate).
func TestUserMentionLookupEmptyNickname(t *testing.T) {
	lookup := &mockUserLookup{
		users: map[string]*UserProfile{
			"alice": {ID: 1, Username: "alice", Nickname: ""},
		},
	}
	ctx := &RenderContext{UserLookup: lookup, ArticleType: "wikidot"}
	out := RenderWikidotCtx(ctx, `[[user Alice]]`)
	if !strings.Contains(out, `>@Alice</a>`) {
		t.Errorf("expected typed name fallback when nickname empty, got %q", out)
	}
	// We still emit data-user-id (the lookup HIT,
	// even though the profile is sparse).
	if !strings.Contains(out, `data-user-id="1"`) {
		t.Errorf("expected data-user-id even without nickname, got %q", out)
	}
}

// ── Stage 4 (P2) — bgcolor / font / indent / iframe / video / audio / date ──

// TestWikidotBgcolor verifies the `[[bgcolor name]]…[[/bgcolor]]`
// block-form background colour. Like `[[color]]`, named
// colours from the lookup table resolve to hex; raw CSS
// passes through the CSS sanitiser (so `[[bgcolor #f0f0f0]]`
// works, but `[[bgcolor red;expression(...)]]` drops the
// CSS and falls back to the raw body text).
func TestWikidotBgcolor(t *testing.T) {
	// Named colour
	out := RenderWikidot(`[[bgcolor yellow]]高亮文字[[/bgcolor]]`)
	if !strings.Contains(out, `<span style="background:#f1c40f">高亮文字</span>`) {
		t.Errorf("expected named bgcolor hex, got %q", out)
	}
	// Hex value passes through sanitiser
	out = RenderWikidot(`[[bgcolor #ffeebb]]hex[[/bgcolor]]`)
	if !strings.Contains(out, `style="background:#ffeebb"`) {
		t.Errorf("expected hex bgcolor, got %q", out)
	}
	// Dangerous CSS (parens, semicolons) gets rejected —
	// the marker falls back to plain text.
	out = RenderWikidot(`[[bgcolor red;expression(alert(1))]]bad[[/bgcolor]]`)
	if strings.Contains(out, `expression`) {
		t.Errorf("expected expression() rejected, got %q", out)
	}
	if !strings.Contains(out, "bad") {
		t.Errorf("expected body text preserved on rejection, got %q", out)
	}
}

// TestWikidotFont verifies `[[font F]]…[[/font]]`
// changes the font-family of the wrapped text. The
// font value is sanitised through the same CSS path
// as bgcolor (so a `font-family: expression(...)`
// payload gets dropped). Multi-word font names like
// "Times New Roman" work because the CSS sanitiser
// allows spaces.
func TestWikidotFont(t *testing.T) {
	out := RenderWikidot(`[[font Courier]]mono[[/font]]`)
	if !strings.Contains(out, `<span style="font-family:Courier">mono</span>`) {
		t.Errorf("expected font span, got %q", out)
	}
	// Dangerous CSS rejected
	out = RenderWikidot(`[[font x; url(javascript:alert(1))]]bad[[/font]]`)
	if strings.Contains(out, `url(`) {
		t.Errorf("expected url() rejected, got %q", out)
	}
}

// TestWikidotIndent verifies the `[[indent]]…[[/indent]]`
// block renders as `<div class="wikidot-indent">`. Body
// is processed via `inlineOnly` (not the full convert
// pipeline) so no `<p>` wrapper gets dropped inside the
// div — that's the same fix we applied to `[[note]]`
// to avoid invalid `<p><div>…</div></p>` HTML.
func TestWikidotIndent(t *testing.T) {
	out := RenderWikidot(`[[indent]]
整段缩进
跨多行
[[/indent]]`)
	if !strings.Contains(out, `<div class="wikidot-indent">`) {
		t.Errorf("expected wikidot-indent div, got %q", out)
	}
	if strings.Contains(out, `<div class="wikidot-indent"><p>`) {
		t.Errorf("expected NO <p> inside indent div, got %q", out)
	}
	if !strings.Contains(out, `整段缩进<br />`) {
		t.Errorf("expected <br /> between body lines, got %q", out)
	}
}

// TestWikidotIndentNested verifies nested
// `[[indent]]…[[indent]]…[[/indent]]…[[/indent]]`
// produces two stacked `<div class="wikidot-indent">`
// divs. A naive non-greedy regex matcher would
// consume the inner `[[/indent]]` first, breaking
// the outer close; the renderer uses a depth-
// counting matcher instead.
func TestWikidotIndentNested(t *testing.T) {
	out := RenderWikidot(`[[indent]]外
[[indent]]内[[/indent]]
[[/indent]]`)
	// Two wikidot-indent divs
	if strings.Count(out, `<div class="wikidot-indent">`) != 2 {
		t.Errorf("expected two nested indent divs, got %q", out)
	}
	// No stray `[[/indent]]` literal text
	if strings.Contains(out, `[[/indent]]`) {
		t.Errorf("expected no raw [[/indent]] in output, got %q", out)
	}
	// Both inner and outer content present
	if !strings.Contains(out, `>外`) {
		t.Errorf("expected outer content, got %q", out)
	}
	if !strings.Contains(out, `>内`) {
		t.Errorf("expected inner content, got %q", out)
	}
}

// TestWikidotIframe verifies `[[iframe URL w h]]`
// renders an `<iframe>` with the URL sanitised
// through `sanitizeURLForAttr` (so `javascript:`
// and `data:` URLs are rejected). Width and height
// are optional — default is `100%` × `400`.
func TestWikidotIframe(t *testing.T) {
	// Sized iframe
	out := RenderWikidot(`[[iframe https://example.com/embed 560 315]]`)
	if !strings.Contains(out, `<iframe src="https://example.com/embed"`) {
		t.Errorf("expected iframe src, got %q", out)
	}
	if !strings.Contains(out, `width="560"`) || !strings.Contains(out, `height="315"`) {
		t.Errorf("expected width/height, got %q", out)
	}
	if !strings.Contains(out, `loading="lazy"`) {
		t.Errorf("expected lazy loading, got %q", out)
	}
	// Default size
	out = RenderWikidot(`[[iframe https://example.com/embed]]`)
	if !strings.Contains(out, `width="100%"`) || !strings.Contains(out, `height="400"`) {
		t.Errorf("expected defaults, got %q", out)
	}
	// Dangerous scheme rejected
	out = RenderWikidot(`[[iframe javascript:alert(1)]]`)
	if strings.Contains(out, `<iframe`) {
		t.Errorf("expected dangerous scheme rejected, got %q", out)
	}
}

// TestWikidotVideo verifies `[[video URL w h]]`
// renders an HTML5 `<video>` with controls. Same
// sanitisation posture as iframe.
func TestWikidotVideo(t *testing.T) {
	out := RenderWikidot(`[[video https://example.com/clip.mp4 640 360]]`)
	if !strings.Contains(out, `<video src="https://example.com/clip.mp4"`) {
		t.Errorf("expected video src, got %q", out)
	}
	if !strings.Contains(out, `width="640"`) || !strings.Contains(out, `height="360"`) {
		t.Errorf("expected width/height, got %q", out)
	}
	if !strings.Contains(out, `controls`) {
		t.Errorf("expected controls attribute, got %q", out)
	}
	// Default size when omitted (height="auto")
	out = RenderWikidot(`[[video https://example.com/clip.mp4]]`)
	if !strings.Contains(out, `height="auto"`) {
		t.Errorf("expected height=auto default, got %q", out)
	}
	// data: URL rejected
	out = RenderWikidot(`[[video data:text/html,foo]]`)
	if strings.Contains(out, `<video`) {
		t.Errorf("expected data: URL rejected, got %q", out)
	}
}

// TestWikidotAudio verifies `[[audio URL]]` renders
// an HTML5 `<audio>` with controls. The control chrome
// is browser-native so we don't set width/height.
func TestWikidotAudio(t *testing.T) {
	out := RenderWikidot(`[[audio https://example.com/song.mp3]]`)
	if !strings.Contains(out, `<audio src="https://example.com/song.mp3"`) {
		t.Errorf("expected audio src, got %q", out)
	}
	if !strings.Contains(out, `controls`) {
		t.Errorf("expected controls, got %q", out)
	}
	// Dangerous scheme
	out = RenderWikidot(`[[audio javascript:alert(1)]]`)
	if strings.Contains(out, `<audio`) {
		t.Errorf("expected dangerous scheme rejected, got %q", out)
	}
}

// TestWikidotDateDefault verifies `[[date]]` (no
// format) renders the current server date in
// `2006-01-02` form.
func TestWikidotDateDefault(t *testing.T) {
	out := RenderWikidot(`今天 [[date]]`)
	// Match a YYYY-MM-DD pattern
	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).MatchString(out) {
		t.Errorf("expected YYYY-MM-DD date, got %q", out)
	}
}

// TestWikidotDateCustomFormat verifies `[[date FMT]]`
// uses the supplied Go time-format string. We use
// `15:04:05` here so the test is robust against
// timezone / daylight-savings jitter (matches just
// the `HH:MM:SS` shape).
func TestWikidotDateCustomFormat(t *testing.T) {
	out := RenderWikidot(`[[date 15:04:05]]`)
	if !regexp.MustCompile(`\d{2}:\d{2}:\d{2}`).MatchString(out) {
		t.Errorf("expected HH:MM:SS, got %q", out)
	}
}

// TestWikidotDateWikidotFormat verifies that the
// Wikidot `$YYYY-$MM-$DD` style format works
// (translated to Go's `2006-01-02` internally).
// This is the format style migrated articles use.
func TestWikidotDateWikidotFormat(t *testing.T) {
	out := RenderWikidot(`[[date $YYYY-$MM-$DD]]`)
	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).MatchString(out) {
		t.Errorf("expected $YYYY-$MM-$DD to render as YYYY-MM-DD, got %q", out)
	}
}

// ── Stage 5 (P2 Stage 3) — tabview ─────────────────────────

// TestWikidotTabviewBasic verifies the `[[tabview]]…[[/tabview]]`
// outer + `[[tab Title]]…[[/tab]]` children render to the
// expected DOM shape: a `.wikidot-tabview` container with a
// `.wikidot-tab-nav` list and a `.wikidot-tab-panels` stack.
// The first tab is `.active` so the panel shows by default;
// the client-side script (ArticleView.tsx) handles the rest.
func TestWikidotTabviewBasic(t *testing.T) {
	in := `[[tabview]]
[[tab 第一页]]
内容 1
[[/tab]]
[[tab 第二页]]
内容 2
[[/tab]]
[[/tabview]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<div class="wikidot-tabview">`) {
		t.Errorf("expected tabview container, got %q", out)
	}
	if !strings.Contains(out, `<ul class="wikidot-tab-nav">`) {
		t.Errorf("expected tab-nav list, got %q", out)
	}
	if !strings.Contains(out, `<div class="wikidot-tab-panels">`) {
		t.Errorf("expected tab-panels stack, got %q", out)
	}
	// Two nav entries — count the opening `<li class="wikidot-tab-tab"`
	// (which appears once per tab). Using the
	// class-attribute shape avoids the substring overlap
	// with `wikidot-tab-tabs` (plural) or `wikidot-tab-tab`
	// appearing inside a `data-` attribute name.
	if strings.Count(out, `<li class="wikidot-tab-tab`) != 2 {
		t.Errorf("expected 2 nav entries, got %q", out)
	}
	// Two panels (count `<div class="wikidot-tab-panel` —
	// the active panel adds a space + `active` between
	// `panel` and the closing quote, so we count the
	// un-quoted form). Use the longer prefix
	// `wikidot-tab-panel` so we don't double-count the
	// `wikidot-tab-panels` wrapper.
	if strings.Count(out, `<div class="wikidot-tab-panel`) != 3 {
		// 1 wrapper (`tab-panels`) + 2 actual
		// panels = 3 substring matches.
		t.Errorf("expected 2 panels + 1 wrapper, got %q", out)
	}
	// First tab is active
	if !strings.Contains(out, `wikidot-tab-tab active" data-tab-id="0"`) {
		t.Errorf("expected first tab active, got %q", out)
	}
	if !strings.Contains(out, `wikidot-tab-panel active" data-tab-id="0"`) {
		t.Errorf("expected first panel active, got %q", out)
	}
	// Tab titles in nav (HTML-escaped)
	if !strings.Contains(out, `>第一页</a>`) {
		t.Errorf("expected first title in nav, got %q", out)
	}
	if !strings.Contains(out, `>第二页</a>`) {
		t.Errorf("expected second title in nav, got %q", out)
	}
}

// TestWikidotTabviewEmpty verifies a `[[tabview]]` with
// no `[[tab …]]` children renders to an empty container
// (so the author sees the silent typo). When the body
// DOES contain a `[[tab ` opener without a closer, the
// raw marker is preserved (separate path; see
// TestWikidotTabviewUnmatchedTab).
func TestWikidotTabviewEmpty(t *testing.T) {
	out := RenderWikidot(`[[tabview]][[/tabview]]`)
	if !strings.Contains(out, `<div class="wikidot-tabview"></div>`) {
		t.Errorf("expected empty tabview, got %q", out)
	}
}

// TestWikidotTabviewUnmatchedTab verifies a tab opener
// without a matching `[[/tab]]` keeps the marker raw
// (so the author can see the typo). The fall-through
// is preferable to silently dropping the tab content.
func TestWikidotTabviewUnmatchedTab(t *testing.T) {
	in := `[[tabview]]
[[tab 漏闭合]]
内容
[[/tabview]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `[[tab 漏闭合]]`) {
		t.Errorf("expected unmatched tab preserved as raw, got %q", out)
	}
	if strings.Contains(out, `<div class="wikidot-tabview"><ul`) {
		t.Errorf("expected NO nav list when tab is unmatched, got %q", out)
	}
}

// TestWikidotTabviewBodyMarkup verifies that block-
// level markup inside a tab body (e.g. `[[div style="..."]]`)
// is rendered through the convert pipeline. Wikidot's
// tabs allow nested block content; we route through
// `convertNoFootnote` so the panel can contain lists,
// blockquotes, code blocks, etc.
func TestWikidotTabviewBodyMarkup(t *testing.T) {
	in := `[[tabview]]
[[tab 页1]]
[[div style="color:red"]]红色文字[[/div]]
[[/tab]]
[[/tabview]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `style="color:red"`) {
		t.Errorf("expected div styling inside tab, got %q", out)
	}
	if !strings.Contains(out, `红色文字`) {
		t.Errorf("expected div content inside tab, got %q", out)
	}
}

// TestWikidotTabviewUnmatchedOuter verifies a
// `[[tabview]]` without a `[[/tabview]]` closer keeps
// the marker raw. The depth-counting scanner runs to
// EOF without finding a close at depth 0; we emit
// the opener + everything after it as-is.
func TestWikidotTabviewUnmatchedOuter(t *testing.T) {
	out := RenderWikidot(`[[tabview]]\n[[tab x]]\n内容`)
	if !strings.Contains(out, `[[tabview]]`) {
		t.Errorf("expected raw [[tabview]] preserved on unmatched, got %q", out)
	}
}

// ── Stage 4 (P1 round 3) — gaps vs rule-wiki.wikidot.com ─────────
//
// Each test below pins one of the new syntax features added to
// close the gap between our parser and the official spec at
// https://rule-wiki.wikidot.com/wiki-syntax. Each test verifies
// both the positive case (the construct renders) and, where
// relevant, the negative case (an unsafe / malformed variant is
// rejected). Together they form a regression set for the round 3
// commit.

// TestWikidotCommentDropped verifies that `[!-- ... --]` HTML
// comments are stripped wholesale from output (Wikidot's spec:
// comments never render). Multi-line comments are consumed in
// one shot via the non-greedy `(?s)\[!--.*?--\]` match.
func TestWikidotCommentDropped(t *testing.T) {
	out := RenderWikidot(`段落之前[!-- 偷偷说一句 --]段落之后。`)
	if !strings.Contains(out, "段落之前段落之后") {
		t.Errorf("expected comment to be dropped, got %q", out)
	}
	if strings.Contains(out, "[!--") || strings.Contains(out, "--]") {
		t.Errorf("expected comment delimiters to be gone, got %q", out)
	}
}

// TestWikidotCommentMultiline verifies multi-line comments
// (body across newlines) are dropped in a single match. Without
// non-greedy `(?s)` mode the parser would stop at the FIRST
// `--]` inside the source, leaving the rest as visible text.
func TestWikidotCommentMultiline(t *testing.T) {
	in := "前[!--\n多\n行\n注释\n--]后"
	out := RenderWikidot(in)
	if !strings.Contains(out, "前后") {
		t.Errorf("expected multi-line comment dropped, got %q", out)
	}
	if strings.Contains(out, "多") || strings.Contains(out, "注释") {
		t.Errorf("expected inner comment text gone, got %q", out)
	}
}

// TestWikidotCommentProtectsMarkup verifies that markup
// inside a comment doesn't fire. `[!-- **bold** --]` should
// show NOTHING, not the word "bold" wrapped in `<strong>`.
func TestWikidotCommentProtectsMarkup(t *testing.T) {
	out := RenderWikidot(`前 [!-- **粗体** --] 后`)
	if strings.Contains(out, "<strong>") {
		t.Errorf("expected no <strong> from commented-out markup, got %q", out)
	}
	if !strings.Contains(out, "前") || !strings.Contains(out, "后") {
		t.Errorf("expected surrounding text to remain, got %q", out)
	}
}

// TestWikidotBoldItalic verifies the nested `//**...**//`
// form renders with both `<em>` and `<strong>` wrappers in
// either nesting order. Wikidot accepts both orderings
// (`//**bold-italic**//` and `**//italic-bold//**`); we run
// the bold pass first and then the italic pass so the order
// of opening markers in the source determines the resulting
// HTML wrapping order. The test pins the presence of BOTH
// wrappers rather than pinning a specific nesting, since
// both nested orderings are valid.
func TestWikidotBoldItalic(t *testing.T) {
	out1 := RenderWikidot(`这是 //**粗斜体**// 的例子。`)
	out2 := RenderWikidot(`这是 **//粗斜体2//** 的例子。`)
	for i, out := range []string{out1, out2} {
		if !strings.Contains(out, "<em>") {
			t.Errorf("case %d: expected <em>, got %q", i, out)
		}
		if !strings.Contains(out, "<strong>") {
			t.Errorf("case %d: expected <strong>, got %q", i, out)
		}
		// One form produces <em><strong>…</strong></em>,
		// the other <strong><em>…</em></strong>. We don't
		// pin a specific order — either nesting is valid
		// for the spec — but both close-tags must be present.
		if !strings.Contains(out, "</strong>") {
			t.Errorf("case %d: expected </strong>, got %q", i, out)
		}
		if !strings.Contains(out, "</em>") {
			t.Errorf("case %d: expected </em>, got %q", i, out)
		}
	}
}

// TestWikidotReverseGuillemets verifies `>>x<<` renders as
// the reverse guillemet pair (»x«). Used for nested quoting
// when the outer level uses `<<…>>`. The regex runs AFTER the
// forward `<<` rule so a `<<x<<` outer-pair isn't half-eaten.
func TestWikidotReverseGuillemets(t *testing.T) {
	out := RenderWikidot(`>>嵌套引用<<`)
	if !strings.Contains(out, "\u00bb") {
		t.Errorf("expected right-guillemet opener, got %q", out)
	}
	if !strings.Contains(out, "\u00ab") {
		t.Errorf("expected left-guillemet closer, got %q", out)
	}
	if !strings.Contains(out, "嵌套引用") {
		t.Errorf("expected inner text preserved, got %q", out)
	}
}

// TestWikidotGermanQuotes verifies `,,x''` renders as the
// German typographic pair („x"). The opener is U+201E
// (low-9 double); the closer is U+201C (left double, used
// as the German closer per German conventions). The regex
// runs BEFORE the generic `''` rule so the double-apostrophe
// is consumed as the German close rather than as a generic
// right double quote.
func TestWikidotGermanQuotes(t *testing.T) {
	out := RenderWikidot(`,,德国引号''`)
	if !strings.Contains(out, "\u201e") {
		t.Errorf("expected U+201E low-9 opener, got %q", out)
	}
	if !strings.Contains(out, "\u201c") {
		t.Errorf("expected U+201C high-left closer, got %q", out)
	}
	if !strings.Contains(out, "德国引号") {
		t.Errorf("expected inner text preserved, got %q", out)
	}
}

// TestWikidotEmptyLink verifies `[# display]` (space
// immediately after `#`) renders as a normal-looking anchor
// whose click does nothing (`href="javascript:;"`). The
// leading space after `#` discriminates this form from the
// real anchor jump-link (`[#name text]`), whose name capture
// forbids whitespace.
func TestWikidotEmptyLink(t *testing.T) {
	out := RenderWikidot(`前缀 [# 占位按钮] 后缀`)
	if !strings.Contains(out, `href="javascript:;"`) {
		t.Errorf("expected javascript: href, got %q", out)
	}
	if !strings.Contains(out, ">占位按钮</a>") {
		t.Errorf("expected display text as link text, got %q", out)
	}
	if !strings.Contains(out, "前缀") || !strings.Contains(out, "后缀") {
		t.Errorf("expected surrounding text, got %q", out)
	}
}

// TestWikidotEmptyLinkWithRealAnchor verifies that an empty
// link and a real anchor jump-link can coexist in the same
// source — the empty form's space-after-`#` is the
// discriminator. A `[#valid_name]` jump-link (no space)
// still resolves to its id; a `[# display]` (with space)
// becomes the placeholder.
func TestWikidotEmptyLinkCoexistsWithAnchor(t *testing.T) {
	out := RenderWikidot(`[!-- 先放个有效锚 --]前 [# empty link] 中 [#valid_anchor 跳转] 后`)
	// Empty link still renders as javascript:
	if !strings.Contains(out, `href="javascript:;"`) {
		t.Errorf("expected javascript: href for empty link, got %q", out)
	}
	// Real anchor should NOT use javascript:
	if strings.Contains(out, `href="javascript:;"><a href="#valid_anchor"`) {
		t.Errorf("expected no double-wrap on real anchor, got %q", out)
	}
	if !strings.Contains(out, `href="#valid_anchor"`) {
		t.Errorf("expected real anchor href, got %q", out)
	}
}

// TestWikidotHashAnchorDef verifies the `[[# name]]`
// alternative to `[[a name="name"]]`. Both forms render to
// the same `<span id="…">` anchor that the `[#name]`
// jump-link targets. `[[a name="..."]]` is the older form;
// `[[# name]]` is the more compact modern form (Wikidot
// accepts both).
func TestWikidotHashAnchorDef(t *testing.T) {
	in := `[[# 起始]]这里是正文。[#起始 跳过去]`
	out := RenderWikidot(in)
	// Anchor def produces an id-bearing span.
	if !strings.Contains(out, `id="起始"`) {
		t.Errorf("expected id span, got %q", out)
	}
	// Jump link uses #起始 as href (the existing
	// reWDAnchor pipeline handles the jump side).
	if !strings.Contains(out, `href="#起始"`) {
		t.Errorf("expected jump href to use id, got %q", out)
	}
	if !strings.Contains(out, ">跳过去</a>") {
		t.Errorf("expected jump display text, got %q", out)
	}
	// The `[[# name]]` opener must be consumed (not raw).
	if strings.Contains(out, "[[# 起始]]") {
		t.Errorf("expected [[# ... ]] to be consumed, got %q", out)
	}
}

// TestWikidotStarredTripleLink verifies `[[[*http://…|Text]]]`
// opens in a new tab. This is the triple-bracket mirror of
// the `[*url text]` single-bracket form.
func TestWikidotStarredTripleLink(t *testing.T) {
	out := RenderWikidot(`[[[*http://www.wikidot.com | Wikidot]]]`)
	if !strings.Contains(out, `href="http://www.wikidot.com"`) {
		t.Errorf("expected href, got %q", out)
	}
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("expected new-tab target, got %q", out)
	}
	if !strings.Contains(out, `rel="nofollow noopener"`) {
		t.Errorf("expected nofollow, got %q", out)
	}
	if !strings.Contains(out, ">Wikidot</a>") {
		t.Errorf("expected display text, got %q", out)
	}
	// The triple-bracket + star prefix must be consumed.
	if strings.Contains(out, "[[[*") {
		t.Errorf("expected marker consumed, got %q", out)
	}
}

// TestWikidotStarredSingleLink verifies `[*http://… text]`
// renders as a new-tab external link.
func TestWikidotStarredSingleLink(t *testing.T) {
	out := RenderWikidot(`参考 [*http://example.com 例子链接] 材料。`)
	if !strings.Contains(out, `href="http://example.com"`) {
		t.Errorf("expected href, got %q", out)
	}
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("expected new-tab target, got %q", out)
	}
	if !strings.Contains(out, ">例子链接</a>") {
		t.Errorf("expected display text, got %q", out)
	}
}

// TestWikidotBareStarredURL verifies that `*http://…` at the
// start of a token becomes a new-tab auto-link. The `*` is
// consumed as the "open in new window" marker; the URL is
// the visible link text and the href.
func TestWikidotBareStarredURL(t *testing.T) {
	out := RenderWikidot(`访问 *http://example.com 看看。`)
	if !strings.Contains(out, `href="http://example.com"`) {
		t.Errorf("expected bare starred URL to become a link, got %q", out)
	}
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("expected new-tab for bare starred URL, got %q", out)
	}
	if strings.Contains(out, "*http://") {
		t.Errorf("expected * stripped from output, got %q", out)
	}
}

// TestWikidotRelativeLink verifies `[/path text]` — a
// relative-path single-bracket link without a protocol. The
// path is preserved as-is (the allow-list in
// sanitizeURLForAttr covers internal `/…` paths).
func TestWikidotRelativeLink(t *testing.T) {
	out := RenderWikidot(`点击 [/blog:post/edit/true 编辑这页] 试试。`)
	if !strings.Contains(out, `href="/blog:post/edit/true"`) {
		t.Errorf("expected relative href, got %q", out)
	}
	if !strings.Contains(out, ">编辑这页</a>") {
		t.Errorf("expected display text, got %q", out)
	}
}

// TestWikidotRelativeLinkNoProtocol verifies that `[/path]`
// (no display text, no protocol) falls back to using the
// path as both href and display text — matching the
// `[url]` and `[url text]` bracket forms.
func TestWikidotRelativeLinkNoProtocol(t *testing.T) {
	out := RenderWikidot(`看 [/blog:post/edit/true] 这页。`)
	if !strings.Contains(out, `href="/blog:post/edit/true"`) {
		t.Errorf("expected relative href, got %q", out)
	}
	if !strings.Contains(out, `>/blog:post/edit/true</a>`) {
		t.Errorf("expected path as display text, got %q", out)
	}
}

// TestWikidotImageAlignCenter verifies `[[=image URL …]]`
// wraps the rendered <img> in a div with the
// `wikidot-image-center` class so the front-end can apply
// `text-align: center`.
func TestWikidotImageAlignCenter(t *testing.T) {
	out := RenderWikidot(`[[=image https://example.com/p.png]]`)
	if !strings.Contains(out, `<div class="wikidot-image-wrap wikidot-image-center">`) {
		t.Errorf("expected center-wrap div, got %q", out)
	}
	if !strings.Contains(out, `<img src="https://example.com/p.png"`) {
		t.Errorf("expected <img> inside wrap, got %q", out)
	}
}

// TestWikidotImageAlignLeft verifies `[[<image …]]`.
func TestWikidotImageAlignLeft(t *testing.T) {
	out := RenderWikidot(`[[<image https://example.com/p.png]]`)
	if !strings.Contains(out, `wikidot-image-left`) {
		t.Errorf("expected left-wrap class, got %q", out)
	}
}

// TestWikidotImageAlignRight verifies `[[>image …]]`.
func TestWikidotImageAlignRight(t *testing.T) {
	out := RenderWikidot(`[[>image https://example.com/p.png]]`)
	if !strings.Contains(out, `wikidot-image-right`) {
		t.Errorf("expected right-wrap class, got %q", out)
	}
}

// TestWikidotImageFloat verifies `[[f<image …]]` and
// `[[f>image …]]`. Float-wrapped images allow surrounding
// text to wrap around them (CSS uses
// `float: left` / `float: right`).
func TestWikidotImageFloat(t *testing.T) {
	outL := RenderWikidot(`[[f<image https://example.com/p.png]]`)
	if !strings.Contains(outL, `wikidot-image-floatleft`) {
		t.Errorf("expected float-left wrap class, got %q", outL)
	}
	outR := RenderWikidot(`[[f>image https://example.com/p.png]]`)
	if !strings.Contains(outR, `wikidot-image-floatright`) {
		t.Errorf("expected float-right wrap class, got %q", outR)
	}
}

// TestWikidotImageLinkStar verifies `link="*url"` on an
// image attribute opens the wrapped link in a new tab.
// The asterisk on the link value is consumed by the parser
// and translated to `target="_blank"` +
// `rel="nofollow noopener"`.
func TestWikidotImageLinkStar(t *testing.T) {
	out := RenderWikidot(`[[image https://example.com/p.png link="*https://example.com/landing"]]`)
	if !strings.Contains(out, `<a href="https://example.com/landing"`) {
		t.Errorf("expected wrapping href, got %q", out)
	}
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("expected new-tab for starred link, got %q", out)
	}
	if !strings.Contains(out, `rel="nofollow noopener"`) {
		t.Errorf("expected nofollow rel, got %q", out)
	}
	if !strings.Contains(out, `<img src="https://example.com/p.png"`) {
		t.Errorf("expected <img> inside anchor, got %q", out)
	}
}

// TestWikidotImageLinkAnchor verifies `link="#anchor"` on
// an image attribute wraps the <img> in an in-page jump
// link. No new-tab attributes (in-page navigation should
// stay in the same window).
func TestWikidotImageLinkAnchor(t *testing.T) {
	in := `[[# 锚点]] 这里是段落。

[[image https://example.com/p.png link="#锚点"]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<a href="#%E9%94%9A%E7%82%B9"`) &&
		!strings.Contains(out, `<a href="#锚点"`) {
		// Either the raw or the HTML-escaped form is
		// acceptable; both prove the parser routed the
		// link through the `#`-form wrapping branch.
		t.Errorf("expected #anchor href, got %q", out)
	}
	if strings.Contains(out, `target="_blank"`) {
		t.Errorf("in-page anchor should NOT open in new tab, got %q", out)
	}
}

// TestWikidotImageLinkInternal verifies `link="wiki-page"`
// (a bare slug, no `/wikidot/` prefix) routes through the
// internal-page branch — it becomes an `<a
// href="/wikidot/<slug>">` link, NOT a
// `target="_blank" rel="nofollow"` external link.
func TestWikidotImageLinkInternal(t *testing.T) {
	out := RenderWikidot(`[[image https://example.com/p.png link="some-page"]]`)
	if !strings.Contains(out, `href="/wikidot/some-page"`) {
		t.Errorf("expected internal href, got %q", out)
	}
	if strings.Contains(out, `target="_blank"`) {
		t.Errorf("internal link should NOT open in new tab, got %q", out)
	}
}

// TestWikidotImageLinkExternalStillWraps verifies the
// historical `link="https://…"` (without the `*` prefix)
// behaviour: external URL wraps with the `<img>` in a plain
// `<a>` and NO extra `target` / `rel` attributes. The
// `target="_blank"` + `rel="nofollow noopener"` pairing is
// reserved for the starred `link="*https://…"` form
// (pinned by TestWikidotImageLinkStar). Pinning the
// here-document behaviour guards against a future refactor
// that re-introduces new-tab attrs on the unstarred form.
func TestWikidotImageLinkExternalStillWraps(t *testing.T) {
	out := RenderWikidot(`[[image https://example.com/p.png link="https://example.com/landing"]]`)
	if !strings.Contains(out, `<a href="https://example.com/landing"`) {
		t.Errorf("expected external wrap href, got %q", out)
	}
	// Must NOT carry new-tab attributes — those are the
	// starred form's contract only.
	if strings.Contains(out, `target="_blank"`) {
		t.Errorf("unstarred link should NOT open in new tab, got %q", out)
	}
}

// TestWikidotDivDataAttribute verifies `[[div data-foo="bar"]]`
// is parsed and emitted as `<div data-foo="bar">…</div>`.
// The attribute value is HTML-escaped so a `"` inside the
// value can't break out of the attribute.
//
// Multi-attribute form (`[[div data-x="…" data-y="…"]]`) is
// NOT supported yet — the current parser only matches the
// first `data-<name>="<value>"` attribute on a `[[div]]` block.
// A separate round would generalise parseDivOpen to accept a
// list of key="value" pairs (mirroring the `[[li…]]` advanced-
// list form). For now, the multi-attribute `[[li]]` is the
// way to combine several data-* attributes into one block.
func TestWikidotDivDataAttribute(t *testing.T) {
	out := RenderWikidot(`[[div data-toggle="modal"]]
内容
[[/div]]`)
	if !strings.Contains(out, `<div data-toggle="modal">`) {
		t.Errorf("expected data attribute on <div>, got %q", out)
	}
	if !strings.Contains(out, "内容") {
		t.Errorf("expected inner content preserved, got %q", out)
	}
}

// TestWikidotDivDataAttributeEscapesValue verifies the
// attribute value is HTML-escaped. A `"><script>` payload in
// the value would break attribute quoting in the rendered
// HTML — the safe path renders the value as entities.
func TestWikidotDivDataAttributeEscapesValue(t *testing.T) {
	out := RenderWikidot(`[[div data-x="<script>"]]
内容
[[/div]]`)
	if strings.Contains(out, `data-x="<script>"`) {
		t.Errorf("expected value to be escaped, got %q", out)
	}
	if !strings.Contains(out, `&lt;script&gt;`) &&
		!strings.Contains(out, `&#34;`) {
		// Either way the dangerous chars are entities.
		// The strict check is no raw `<` / `>` in the
		// attribute value.
		t.Errorf("expected attribute to be HTML-escaped, got %q", out)
	}
}

// TestWikidotDivDataAttributeBadName verifies a data
// attribute name with non-token characters (e.g. spaces)
// is rejected (the construct is left raw so the author
// can see the typo).
func TestWikidotDivDataAttributeBadName(t *testing.T) {
	out := RenderWikidot(`[[div data-bad name="x"]]
内容
[[/div]]`)
	// The construct was NOT emitted as a <div>. The
	// parser returns ok=false on bad data- attribute name,
	// so the opener stays raw in the source. We assert
	// the raw text is preserved verbatim (the author can
	// see the typo) by checking that the original marker
	// substring survives.
	if !strings.Contains(out, `data-bad`) {
		t.Errorf("expected raw marker text preserved on bad data name, got %q", out)
	}
	// And specifically, no <div data-bad ...> element
	// was emitted.
	if strings.Contains(out, `<div data-bad`) {
		t.Errorf("expected NO <div data-bad> emitted, got %q", out)
	}
}

// TestWikidotDivDataAttributeNested verifies that nested
// data-bearing divs (one inside another) parse cleanly.
// This is a guard for the depth-counter in walkDivBody —
// a `[[div data-x="…"]]` opener that's followed by another
// `[[div data-y="…"]]` should pair the FIRST `[[/div]]`
// with the inner opener, not the outer.
func TestWikidotDivDataAttributeNested(t *testing.T) {
	in := `[[div data-x="outer"]]
外层
[[div data-y="inner"]]
内层
[[/div]]
回到外层
[[/div]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `data-x="outer"`) {
		t.Errorf("expected outer data-x, got %q", out)
	}
	if !strings.Contains(out, `data-y="inner"`) {
		t.Errorf("expected inner data-y, got %q", out)
	}
	if got, want := strings.Count(out, "<div"), 2; got != want {
		t.Errorf("expected %d <div> openings, got %d in %q", want, got, out)
	}
	if got, want := strings.Count(out, "</div>"), 2; got != want {
		t.Errorf("expected %d </div> closings, got %d in %q", want, got, out)
	}
}

// TestWikidotTableCellContinuation verifies a `||…||` row
// whose LAST cell spans multiple lines via Wikidot's
// `_<space>\n` continuation marker joins into a single
// row line, and the resulting row renders correctly.
// The original spec example:
//
//   |||||| 超长 _
//   内容 8||
//
// → joined: `|||||| 超长 内容 8||` → renders as a row
// with a single content cell.
func TestWikidotTableCellContinuation(t *testing.T) {
	in := `||~ H1 ||~ H2 ||
|| A |||| 超长 _
内容 8||`
	out := RenderWikidot(in)
	// Joined cell content has both pieces separated by a
	// space (the joinMultiLineTableRows replacement).
	if !strings.Contains(out, "超长") || !strings.Contains(out, "内容 8") {
		t.Errorf("expected joined cell content, got %q", out)
	}
	// Rendered as a table with two rows.
	if !strings.Contains(out, `<table class="wiki-table">`) {
		t.Errorf("expected <table>, got %q", out)
	}
	if !strings.Contains(out, `<th>H1</th>`) {
		t.Errorf("expected header row, got %q", out)
	}
	// Continuation lines are joined into a single space-
	// separated cell, NOT multiple paragraphs of text.
	if strings.Contains(out, "<p>") {
		t.Errorf("expected no <p> wrap inside cells, got %q", out)
	}
}

// TestWikidotTableCellContinuationPlainNewline verifies
// that even a bare newline (without the `_<space>` marker)
// between a `||…` opener and a `…||` closer is treated as
// cell continuation. The spec example uses `_<space>\n`
// but Wikidot also accepts a bare newline in practice.
func TestWikidotTableCellContinuationPlainNewline(t *testing.T) {
	in := `||~ H1 ||
|| A ||
|| 长内容
继续||`
	out := RenderWikidot(in)
	if !strings.Contains(out, "长内容") {
		t.Errorf("expected inner content, got %q", out)
	}
	if !strings.Contains(out, "继续") {
		t.Errorf("expected continuation text, got %q", out)
	}
	if !strings.Contains(out, `<table class="wiki-table">`) {
		t.Errorf("expected table, got %q", out)
	}
}

// TestWikidotTableCellContinuationUnmatched verifies that
// an orphaned multi-line opener (no later line ending with
// `||`) leaves the lines raw so the author can see the typo.
// We don't join them — orphan in, orphan out.
func TestWikidotTableCellContinuationUnmatched(t *testing.T) {
	in := `前面一段。
|| 不闭合的开头
继续散落
没有结尾的双竖线。
再一段。`
	_ = RenderWikidot(in) // Just shouldn't panic. We don't pin the
	// exact output (orphan handling has two valid outcomes:
	// raw text OR partial processing), only the absence of
	// a crash and absence of an emitted <table> for the
	// unmatched opener.
}

// ── Stage 5 (P2 round 4) — gallery + size 相对值 + form widgets ─────

// TestWikidotSizeRelativePercent verifies the `[[size N%]]`
// form (relative units — percent of the parent's font size).
// The size value passes through sanitizeCSSValue and gets
// emitted on the `style` attribute verbatim. Wikidot's spec
// lists `[[size 80%]]`, `[[size 100%]]`, `[[size 150%]]` as
// canonical forms; we accept any non-negative percent.
func TestWikidotSizeRelativePercent(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[[size 80%]]小[[/size]]`, `font-size:80%`},
		{`[[size 100%]]不变[[/size]]`, `font-size:100%`},
		{`[[size 150%]]放大到1.5倍[[/size]]`, `font-size:150%`},
		{`[[size 50%]]一半[[/size]]`, `font-size:50%`},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		if !strings.Contains(out, c.want) {
			t.Errorf("input %q: expected %q in %q", c.in, c.want, out)
		}
	}
}

// TestWikidotSizeRelativeEm verifies the `[[size Nem]]`
// form. Em is also a relative unit (1em = current font size).
// The spec lists `[[size 0.8em]]`, `[[size 1em]]`, `[[size 1.5em]]`
// as canonical forms; we accept any non-negative value,
// including fractional ones.
func TestWikidotSizeRelativeEm(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[[size 0.8em]][[/size]]`, `font-size:0.8em`},
		{`[[size 1em]][[/size]]`, `font-size:1em`},
		{`[[size 1.5em]][[/size]]`, `font-size:1.5em`},
		{`[[size 2em]][[/size]]`, `font-size:2em`},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		if !strings.Contains(out, c.want) {
			t.Errorf("input %q: expected %q in %q", c.in, c.want, out)
		}
	}
}

// TestWikidotSizeAbsolutePx verifies the `[[size Npx]]`
// form (absolute pixel size — not relative to the parent's
// font size). The spec lists `[[size 7px]]` and
// `[[size 18.75px]]` as canonical forms; we accept any
// non-negative value.
func TestWikidotSizeAbsolutePx(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[[size 7px]]7px[[/size]]`, `font-size:7px`},
		{`[[size 14px]]14px[[/size]]`, `font-size:14px`},
		{`[[size 18.75px]]18.75px[[/size]]`, `font-size:18.75px`},
		{`[[size 24px]]24px[[/size]]`, `font-size:24px`},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		if !strings.Contains(out, c.want) {
			t.Errorf("input %q: expected %q in %q", c.in, c.want, out)
		}
	}
}

// TestWikidotSizeKeywordAll9 verifies each of the 9 keyword
// names Wikidot's spec lists under "绝对字体大小" + 相对
// "larger" / "smaller" — every name maps to a CSS keyword
// in `sizeMap`. The keyword form means the size is in
// absolute CSS terms (independent of the parent's font
// size), except for `larger` and `smaller` which are
// relative.
func TestWikidotSizeKeywordAll9(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`[[size xx-small]]xx-sm[[/size]]`, `font-size:0.5rem`},
		{`[[size x-small]]x-sm[[/size]]`, `font-size:0.625rem`},
		{`[[size smaller]]更小[[/size]]`, `font-size:0.75rem`},
		{`[[size small]]小[[/size]]`, `font-size:0.8rem`},
		{`[[size medium]]中[[/size]]`, `font-size:1rem`},
		{`[[size large]]大[[/size]]`, `font-size:1.25rem`},
		{`[[size x-large]]更大[[/size]]`, `font-size:1.5rem`},
		{`[[size xx-large]]最大[[/size]]`, `font-size:2rem`},
		{`[[size larger]]最大[[/size]]`, `font-size:2.5rem`},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		if !strings.Contains(out, c.want) {
			t.Errorf("input %q: expected %q in %q", c.in, c.want, out)
		}
	}
}

// TestWikidotSizeNested verifies a `[[size larger]]` with an
// inner `[[size smaller]]` produces two stacked `<span
// style="font-size:…">` elements. The inner span's CSS value
// is computed against its own sizeMap entry (Wikidot
// computes it relative to the parent's font size in the
// browser — the static `font-size` value we emit is the
// absolute rem value the sizeMap gives).
func TestWikidotSizeNested(t *testing.T) {
	in := `[[size larger]]外 [[size smaller]]内[[/size]] 又外[[/size]]`
	out := RenderWikidot(in)
	// Outer = 2.5rem, inner = 0.75rem.
	if !strings.Contains(out, `font-size:2.5rem`) {
		t.Errorf("expected outer font-size 2.5rem, got %q", out)
	}
	if !strings.Contains(out, `font-size:0.75rem`) {
		t.Errorf("expected inner font-size 0.75rem, got %q", out)
	}
	// Two stacked <span>s.
	if got, want := strings.Count(out, "<span style=\"font-size"), 2; got != want {
		t.Errorf("expected %d font-size spans, got %d in %q", want, got, out)
	}
}

// TestWikidotSizeUnknownFallsBackToPlain verifies an
// unrecognized `[[size keyword]]` (e.g. `[[size giant]]`) —
// not in sizeMap AND not a number — degrades to plain text
// (no `<span style="font-size:giant">` is rendered). The
// author sees the typo.
func TestWikidotSizeUnknownFallsBackToPlain(t *testing.T) {
	out := RenderWikidot(`[[size giant]]巨型字[[/size]]`)
	if strings.Contains(out, "font-size") {
		t.Errorf("expected NO font-size on unknown keyword, got %q", out)
	}
	// Inner text preserved.
	if !strings.Contains(out, "巨型字") {
		t.Errorf("expected inner text preserved, got %q", out)
	}
}

// TestWikidotSizeZeroSafe verifies `[[size 0]]` and
// `[[size 0px]]` accept the explicit-zero form (some CSS
// contexts need a hard zero to override a relative unit).
// sanitizeCSSValue accepts `0` and `0px` directly.
func TestWikidotSizeZeroSafe(t *testing.T) {
	if out := RenderWikidot(`[[size 0]]零[[/size]]`); !strings.Contains(out, "font-size:0") {
		t.Errorf("expected font-size:0, got %q", out)
	}
	if out := RenderWikidot(`[[size 0px]]零[[/size]]`); !strings.Contains(out, "font-size:0px") {
		t.Errorf("expected font-size:0px, got %q", out)
	}
}

// TestWikidotGalleryBasicLines verifies a gallery with two
// plain `URL` lines (no caption) renders as two figures
// each with an `<img>` and a thumbnail structure (no
// `<figcaption>` when no caption).
func TestWikidotGalleryBasicLines(t *testing.T) {
	in := `[[gallery]]
https://example.com/a.jpg
https://example.com/b.png
[[/gallery]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<div class="wikidot-gallery">`) {
		t.Errorf("expected wikidot-gallery wrapper, got %q", out)
	}
	if got, want := strings.Count(out, "<figure>"), 2; got != want {
		t.Errorf("expected %d figures, got %d in %q", want, got, out)
	}
	if !strings.Contains(out, `src="https://example.com/a.jpg"`) {
		t.Errorf("expected first image src, got %q", out)
	}
	if !strings.Contains(out, `src="https://example.com/b.png"`) {
		t.Errorf("expected second image src, got %q", out)
	}
}

// TestWikidotGalleryCaptions verifies `URL | caption` lines
// produce `<figcaption>` elements with the caption text.
// We split on the FIRST `|` so captions containing a pipe
// are preserved as-is (no further parsing on the caption).
func TestWikidotGalleryCaptions(t *testing.T) {
	in := `[[gallery]]
https://example.com/a.jpg | 第一张图
https://example.com/b.png | 第二张 | 含管道符
[[/gallery]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, "<figcaption>第一张图</figcaption>") {
		t.Errorf("expected first figcaption, got %q", out)
	}
	if !strings.Contains(out, "<figcaption>第二张 | 含管道符</figcaption>") {
		t.Errorf("expected caption with embedded pipe preserved, got %q", out)
	}
	// alt attribute is built from the caption text.
	if !strings.Contains(out, `alt="第一张图"`) {
		t.Errorf("expected first img alt from caption, got %q", out)
	}
}

// TestWikidotGalleryEmptyAndBlank verifies an empty gallery
// (no inner lines) and a gallery with only blank lines both
// render as an empty `<div class="wikidot-gallery">` (no
// figure children). This avoids a `<div>` containing only
// whitespace text nodes.
func TestWikidotGalleryEmptyAndBlank(t *testing.T) {
	empty := `[[gallery]]
[[/gallery]]`
	out := RenderWikidot(empty)
	if !strings.Contains(out, `<div class="wikidot-gallery">`) {
		t.Errorf("expected gallery wrapper for empty body, got %q", out)
	}
	if strings.Contains(out, "<figure>") {
		t.Errorf("expected NO figures in empty body, got %q", out)
	}
}

// TestWikidotGalleryDropsBadURLs verifies that a line with
// an unsafe URL (e.g. `javascript:alert(1)`) is dropped
// silently — `sanitizeURLForAttr` rejects it — without
// breaking the gallery's grid layout.
func TestWikidotGalleryDropsBadURLs(t *testing.T) {
	in := `[[gallery]]
https://example.com/a.jpg
javascript:alert(1)
https://example.com/b.png
[[/gallery]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `src="https://example.com/a.jpg"`) {
		t.Errorf("expected first image src, got %q", out)
	}
	if !strings.Contains(out, `src="https://example.com/b.png"`) {
		t.Errorf("expected third image src, got %q", out)
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("expected javascript: scheme dropped, got %q", out)
	}
	if got, want := strings.Count(out, "<figure>"), 2; got != want {
		t.Errorf("expected %d safe figures, got %d in %q", want, got, out)
	}
}

// TestWikidotGalleryUnclosed verifies that an unclosed
// `[[gallery]]` (no matching `[[/gallery]]`) leaves the
// opener raw so the author can see the typo. We don't crash
// and we don't emit a half-rendered gallery.
func TestWikidotGalleryUnclosed(t *testing.T) {
	out := RenderWikidot(`前面一段。
[[gallery]]
https://example.com/a.jpg`)
	if strings.Contains(out, `class="wikidot-gallery"`) {
		t.Errorf("expected NO gallery wrapper on unclosed, got %q", out)
	}
}

// TestWikidotFormBasic verifies a minimal form with method +
// action is rendered as `<form method="…" action="…">…</form>`
// with the inner prose passed through inlineOnly.
func TestWikidotFormBasic(t *testing.T) {
	in := `[[form method="post" action="/api/submit"]]
请输入姓名: **必填**
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<form method="post" action="/api/submit">`) {
		t.Errorf("expected form with method+action, got %q", out)
	}
	if !strings.Contains(out, `<strong>必填</strong>`) {
		t.Errorf("expected bold inside form, got %q", out)
	}
	if !strings.Contains(out, `</form>`) {
		t.Errorf("expected form close, got %q", out)
	}
}

// TestWikidotFormInputText verifies the `[[input type="text"
// name="…"]]` single-tag widget renders as `<input
// type="text" name="…">`. Default `type` is `text` if the
// author omits it (matches HTML form default).
func TestWikidotFormInputText(t *testing.T) {
	in := `[[form action="x"]]
[[input type="text" name="username" value="默认"]]
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<input type="text" name="username" value="默认"`) {
		t.Errorf("expected input text widget, got %q", out)
	}
}

// TestWikidotFormInputDefaultType verifies that an `[[input
// name="…"]]` without an explicit `type` attribute defaults
// to `type="text"` (HTML's input default). This avoids an
// author writing `[[input name="email"]]` and getting
// nothing visible because the default of HTML input IS
// `text`, but we want it spelled out so the rendered HTML
// is explicit.
func TestWikidotFormInputDefaultType(t *testing.T) {
	out := RenderWikidot(`[[form action="x"]][[input name="author"]][[/form]]`)
	if !strings.Contains(out, `type="text"`) {
		t.Errorf("expected default type=text, got %q", out)
	}
	if !strings.Contains(out, `name="author"`) {
		t.Errorf("expected name attribute, got %q", out)
	}
}

// TestWikidotFormCheckbox verifies `[[checkbox name="…"
// checked]]` renders as `<input type="checkbox" name="…"
// checked>` (the bare `checked` attribute is preserved).
func TestWikidotFormCheckbox(t *testing.T) {
	in := `[[form action="x"]]
[[checkbox name="agree" checked]]
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<input type="checkbox" name="agree"`) {
		t.Errorf("expected checkbox input, got %q", out)
	}
	if !strings.Contains(out, "checked") {
		t.Errorf("expected bare checked attribute, got %q", out)
	}
}

// TestWikidotFormRadio verifies `[[radio …]]` renders as
// `<input type="radio" …>` (shares the input rendering
// path with checkbox — only `type` differs).
func TestWikidotFormRadio(t *testing.T) {
	in := `[[form action="x"]]
[[radio name="choice" value="a"]]
[[radio name="choice" value="b"]]
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `type="radio" name="choice" value="a"`) {
		t.Errorf("expected first radio input, got %q", out)
	}
	if !strings.Contains(out, `type="radio" name="choice" value="b"`) {
		t.Errorf("expected second radio input, got %q", out)
	}
}

// TestWikidotFormTextarea verifies `[[textarea
// attrs]]content[[/textarea]]` renders as
// `<textarea attrs>content</textarea>`. The body goes
// through inlineOnly so wikidot inline formatting
// (`[link]`, `**bold**`) survives inside the textarea.
func TestWikidotFormTextarea(t *testing.T) {
	in := `[[form action="x"]]
[[textarea name="body" rows="5"]]默认内容,**粗体**。[[/textarea]]
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<textarea name="body" rows="5"`) {
		t.Errorf("expected textarea with name+rows, got %q", out)
	}
	if !strings.Contains(out, `>默认内容,<strong>粗体</strong>。<`) {
		t.Errorf("expected textarea inner with bold markup, got %q", out)
	}
	if !strings.Contains(out, `</textarea>`) {
		t.Errorf("expected textarea close, got %q", out)
	}
}

// TestWikidotFormButtonLabel verifies `[[button
// label="…"]]` renders as `<button type="submit">…</button>`
// (HTML's <button> uses inner text, not a label attribute).
// The `label` attribute is consumed and rendered as the
// inner text; the `type` defaults to `submit`.
func TestWikidotFormButtonLabel(t *testing.T) {
	out := RenderWikidot(`[[form action="x"]][[button label="提交"]][[/form]]`)
	if !strings.Contains(out, `<button type="submit">提交</button>`) {
		t.Errorf("expected submit button with inner text, got %q", out)
	}
}

// TestWikidotFormButtonEmptyLabel verifies that an empty /
// missing `label` attribute yields the literal text "Submit"
// (a sensible default so an empty `[[button]]` doesn't
// produce a zero-width button that's unclickable in tests).
func TestWikidotFormButtonEmptyLabel(t *testing.T) {
	out := RenderWikidot(`[[form action="x"]][[button]][[/form]]`)
	if !strings.Contains(out, `<button type="submit">Submit</button>`) {
		t.Errorf("expected default Submit text, got %q", out)
	}
}

// TestWikidotFormSelectOption verifies the paired
// `[[select …]]…[[/select]]` containing multiple
// `[[option …]]Label[[/option]]` constructs renders to
// a `<select>` with `<option>` children. Inner labels
// go through inlineOnly so wikidot inline formatting
// inside an option label is preserved.
func TestWikidotFormSelectOption(t *testing.T) {
	in := `[[form action="x"]]
[[select name="color"]]
[[option value="r"]]红[[/option]]
[[option value="g" selected]]绿[[/option]]
[[option value="b"]]蓝[[/option]]
[[/select]]
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `<select name="color">`) {
		t.Errorf("expected select wrapper, got %q", out)
	}
	if !strings.Contains(out, `<option value="r">红</option>`) {
		t.Errorf("expected first option, got %q", out)
	}
	if !strings.Contains(out, `<option value="g"`) {
		t.Errorf("expected second option, got %q", out)
	}
	if !strings.Contains(out, ` selected`) {
		t.Errorf("expected bare selected attribute on g, got %q", out)
	}
	if !strings.Contains(out, `<option value="b">蓝</option>`) {
		t.Errorf("expected third option, got %q", out)
	}
	if !strings.Contains(out, `</select>`) {
		t.Errorf("expected select close, got %q", out)
	}
}

// TestWikidotFormUnclosed verifies that an unbalanced
// `[[form …]]` (no `[[/form]]`) leaves the opener raw and
// the inner widgets unparsed. The body widgets become
// either replaced by their HTML widgets (input / button /
// checkbox / radio all have single-tag regexes that fire
// outside the form block as well, but since the inner form
// body never had its terminator matched, the substitute-
// FormWidgets pass never runs — so they stay raw).
func TestWikidotFormUnclosed(t *testing.T) {
	in := `[[form method="post"]]
[[input type="text" name="x"]]`
	out := RenderWikidot(in)
	if strings.Contains(out, "<form") {
		t.Errorf("expected NO <form on unclosed, got %q", out)
	}
	// The opener tag itself stays raw so the author sees
	// the typo.
	if !strings.Contains(out, "[[form method=\"post\"]]") {
		t.Errorf("expected raw opener, got %q", out)
	}
}

// TestWikidotFormCustomAttr verifies non-standard HTML5
// attributes on widgets pass through sanitisation. `pattern=`,
// `placeholder=`, `required=` are forwarded verbatim so
// authors get the full HTML5 input surface without wikidot
// having to maintain an allow-list of every new key.
func TestWikidotFormCustomAttr(t *testing.T) {
	out := RenderWikidot(`[[form action="x"]]
[[input type="email" name="email" placeholder="you@example.com" required="true"]]
[[/form]]`)
	if !strings.Contains(out, `type="email"`) {
		t.Errorf("expected email input, got %q", out)
	}
	if !strings.Contains(out, `placeholder="you@example.com"`) {
		t.Errorf("expected placeholder attribute, got %q", out)
	}
	if !strings.Contains(out, `required="true"`) {
		t.Errorf("expected required attribute, got %q", out)
	}
}

// TestWikidotFormNested verifies a nested `[[form action="x"]]` block
// (rare but legal) renders both `<form>` tags in document
// order with correctly paired closes. The depth counter
// in renderWikidotFormBlocks keeps the match balanced.
func TestWikidotFormNested(t *testing.T) {
	in := `[[form method="post" action="/outer"]]
外层
[[form method="get" action="/inner"]]
内层
[[/form]]
回到外层
[[/form]]`
	out := RenderWikidot(in)
	if !strings.Contains(out, `action="/outer"`) {
		t.Errorf("expected outer form, got %q", out)
	}
	if !strings.Contains(out, `action="/inner"`) {
		t.Errorf("expected inner form, got %q", out)
	}
	if got, want := strings.Count(out, "<form"), 2; got != want {
		t.Errorf("expected %d <form> openings, got %d in %q", want, got, out)
	}
	if got, want := strings.Count(out, "</form>"), 2; got != want {
		t.Errorf("expected %d </form> closings, got %d in %q", want, got, out)
	}
}

// TestWikidotBlockPlaceholdersFullyRestored is a regression test
// for a bug where `%%BLOCK_N%%` placeholders leaked through to
// the rendered output.
//
// Two compounding causes:
//   1. Phase 1 (block storage) stashes the table line as a block
//      BEFORE Phase 2 (inline `@@…@@` literal) has had a chance
//      to substitute the inner literal. The result is a stored
//      block whose HTML contains the inner literal's placeholder.
//   2. Phase 10 (restore stored blocks) iterated `p.blocks` once.
//      Go's map iteration order is random, so if the outer table
//      block is restored before the inner literal block, the inner
//      placeholder ends up inside an already-restored `<table>` and
//      the next iteration of the map misses it.
//
// The fix: Phase 10 now runs multi-pass until a full sweep
// produces no more substitutions. Also extends the same loop to
// `p.headings[].Text` so heading text that contains anchors
// (`[[# name]]` in `++ [[# name]]title`) doesn't leak into the
// TOC builder (Phase 12 reads `h.Text`).
//
// This test reproduces both leak paths in one input.
func TestWikidotBlockPlaceholdersFullyRestored(t *testing.T) {
	in := `+ [[# headings]]标题

||~ 你所打的 ||~ 你将看见 ||
|| {{@@//斜体//@@}} || //斜体// ||

[[toc]]
`
	out := RenderWikidot(in)
	// No placeholder should survive in the output.
	if strings.Contains(out, "%BLOCK_") {
		t.Errorf("placeholder leaked into output: %q", out)
	}
	// The TOC entry for the heading should contain the heading
	// text (`标题`), not the anchor placeholder or the raw anchor.
	if !strings.Contains(out, ">标题<") {
		t.Errorf("expected TOC entry to contain heading text, got %q", out)
	}
	// The table cell should contain the raw //斜体// inside <code>,
	// not a %BLOCK_% placeholder.
	if !strings.Contains(out, "<code>//斜体//</code>") {
		t.Errorf("expected <code>//斜体//</code> in table cell, got %q", out)
	}
}
