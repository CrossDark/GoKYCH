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
