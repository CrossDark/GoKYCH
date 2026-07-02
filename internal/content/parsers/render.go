package parsers

import (
	"context"
	"html"
	"html/template"
	"log/slog"
	"strings"

	"gokych/internal/typst"
)

// ArticleType enumerates the supported article content types.
type ArticleType string

const (
	TypeMarkdown ArticleType = "md"
	TypeWikidot  ArticleType = "wikidot"
	TypeHTML     ArticleType = "html"
	TypeBBCode   ArticleType = "bbcode"
	TypeTypst    ArticleType = "typst"
)

// Render converts raw source of the given type into safe HTML. articleID is
// used only by the typst renderer to key its DB cache (see renderTypst).
func Render(at ArticleType, articleID int, source string) template.HTML {
	return RenderCtx(at, articleID, source, nil)
}

// RenderCtx is the side-channel-aware variant of Render. ctx may be nil
// (in which case it behaves identically to Render). Only the wikidot
// renderer currently consults ctx — `[[include]]`, `[[module]]`,
// `%%var%%`, `[[toc]]`, and footnote interlink resolution all
// short-circuit to raw source when there's no PageLookup, so passing
// nil is safe for the static subset.
//
// After the type-specific renderer converts markup to HTML, the result
// flows through PostProcessArticleHTML which (once per render) handles
// sanitisation, YouTube-placeholder → iframe, image lazy loading,
// external link attributes, data-line numbering, and wrapper markup.
// That means EVERY article type — including typst — leaves this
// function as a fully-prepared HTML blob that the client can display
// without running any DOM mutations.
func RenderCtx(at ArticleType, articleID int, source string, ctx *RenderContext) template.HTML {
	var raw string
	var extraClass string
	switch at {
	case TypeMarkdown:
		raw = RenderMarkdown(source)
	case TypeWikidot:
		raw = RenderWikidotCtx(ctx, source)
	case TypeBBCode:
		raw = RenderBBCode(source)
	case TypeHTML:
		raw = source // trusted admin content (sanitised by PostProcessArticleHTML)
	case TypeTypst:
		raw = renderTypst(ctx, articleID, source)
		extraClass = "typst-content" // Typst-specific CSS hooks (MathML alignment, layout)
	default:
		raw = "<p>不支持的格式。</p>"
	}
	return template.HTML(PostProcessArticleHTML(raw, extraClass))
}

// RenderLine breaks a multi-line source into lines that match the original
// source line numbering (for line comments). It returns rendered HTML for
// the full source plus a per-line HTML slice. For wikidot/bbcode each line is
// rendered individually (inline-only contexts). typst lines compile standalone
// WITHOUT touching the whole-article cache, so a single line can't overwrite
// the cached full-document HTML keyed on articleID.
func RenderLines(at ArticleType, articleID int, source string) (full template.HTML, lines []template.HTML) {
	full = Render(at, articleID, source)
	rawLines := strings.Split(source, "\n")
	lines = make([]template.HTML, len(rawLines))
	for i, l := range rawLines {
		if at == TypeTypst {
			lines[i] = template.HTML(renderTypstUncached(l))
		} else {
			lines[i] = Render(at, articleID, l)
		}
	}
	return
}

// IsValidType reports whether t is a recognized article type.
func IsValidType(t string) bool {
	switch ArticleType(t) {
	case TypeMarkdown, TypeWikidot, TypeHTML, TypeBBCode, TypeTypst:
		return true
	}
	return false
}

// sanitizeHTML does basic safety stripping for non-admin HTML content.
// Currently a no-op because admin-authored HTML is trusted.
func sanitizeHTML(s string) string { return s }

// renderTypst reads the cached HTML for articleID from typst_cache. The
// compile happens at publish time (see typst.Worker.CompileAndCache), so
// this path is a pure SELECT — readers never fork the typst CLI. A cache
// miss (e.g. an article created before the precompile pipeline was added)
// is surfaced as a "pending compile" placeholder, NOT a fallback compile,
// because doing the compile here would defeat the whole point of the
// performance optimisation.
//
// The Worker is taken from ctx.Typst; if ctx is nil or ctx.Typst is nil
// (e.g. a test or a code path that doesn't carry the worker), the function
// returns the "compile pending" placeholder instead of panicking — the
// old package-level-db fallback behaved similarly when SetDB hadn't been
// called.
//
// The returned HTML is already fully post-processed at compile time:
// wrapped in <div class="typst-content" data-typst="1">, with data-line
// numbers assigned to block elements, lazy attributes on images, and
// external link attributes applied (see typst.postprocessTypedHTML).
// The client-side hydration detects the data-typst marker and skips
// DOMPurify/KaTeX/mermaid entirely — zero DOM mutations on the reader's
// main thread.
func renderTypst(ctx *RenderContext, articleID int, source string) string {
	if ctx == nil || ctx.Typst == nil {
		return `<p><em>本文档尚未编译完成,请稍后再试,或联系管理员重新发布。</em></p>`
	}
	if !typst.Available() {
		return `<p><em>Typst 编译器未安装,无法渲染本文。</em></p>`
	}
	rctx := ctx.Ctx
	if rctx == nil {
		rctx = context.Background()
	}
	body, err := ctx.Typst.CompileHTMLCachedCtx(rctx, articleID, source)
	if err != nil {
		if strings.Contains(err.Error(), "no cached HTML") {
			slog.Info("typst render: cache miss", "article_id", articleID)
			return `<p><em>本文档尚未编译完成,请稍后再试,或联系管理员重新发布。</em></p>`
		}
		slog.Error("typst render", "article_id", articleID, "err", err)
		return `<p><em>Typst 渲染失败:` + html.EscapeString(err.Error()) + `</em></p>`
	}
	return body
}

// renderTypstUncached compiles a single line of typst without the DB cache,
// used by RenderLines so per-line output never overwrites the cached
// full-document HTML.
func renderTypstUncached(source string) string {
	if !typst.Available() {
		return "<p><em>Typst 编译器未安装。</em></p>"
	}
	body, err := typst.CompileHTML(source)
	if err != nil {
		slog.Error("typst line compile", "err", err)
		return "<p><em>Typst 编译失败：" + html.EscapeString(err.Error()) + "</em></p>"
	}
	return body
}

func init() {
	slog.Info("loaded markdown, wikidot, bbcode, html renderers")
}
