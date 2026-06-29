package parsers

import (
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
func RenderCtx(at ArticleType, articleID int, source string, ctx *RenderContext) template.HTML {
	var html string
	switch at {
	case TypeMarkdown:
		html = RenderMarkdown(source)
	case TypeWikidot:
		html = RenderWikidotCtx(ctx, source)
	case TypeBBCode:
		html = RenderBBCode(source)
	case TypeHTML:
		html = source // trusted admin content
	case TypeTypst:
		html = renderTypst(articleID, source)
	default:
		html = "<p>不支持的格式。</p>"
	}
	return template.HTML(html)
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
// compile happens at publish time (see typst.CompileAndCache), so this
// path is a pure SELECT — readers never fork the typst CLI. A cache miss
// (e.g. an article created before the precompile pipeline was added) is
// surfaced as a "pending compile" placeholder, NOT a fallback compile,
// because doing the compile here would defeat the whole point of the
// performance optimisation.
func renderTypst(articleID int, source string) string {
	if !typst.Available() {
		return `<p><em>Typst 编译器未安装,无法渲染本文。</em></p>`
	}
	body, err := typst.CompileHTMLCached(articleID, source)
	if err != nil {
		// Cache miss is the expected "first view, no publish yet" state; log
		// at Info, not Error, so the operator's log doesn't light up red
		// for every legacy article that pre-dates precompile.
		if strings.Contains(err.Error(), "no cached HTML") {
			slog.Info("typst render: cache miss", "article_id", articleID)
			return `<p><em>本文档尚未编译完成,请稍后再试,或联系管理员重新发布。</em></p>`
		}
		slog.Error("typst render", "article_id", articleID, "err", err)
		return `<p><em>Typst 渲染失败:` + err.Error() + `</em></p>`
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
		return "<p><em>Typst 编译失败：" + err.Error() + "</em></p>"
	}
	return body
}

func init() {
	slog.Info("loaded markdown, wikidot, bbcode, html renderers")
}
