package parsers

import (
	"html/template"
	"log"
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
	var html string
	switch at {
	case TypeMarkdown:
		html = RenderMarkdown(source)
	case TypeWikidot:
		html = RenderWikidot(source)
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

// renderTypst compiles typst source to HTML via the typst CLI, consulting the
// typst_cache table keyed on articleID. Falls back to a placeholder if
// compilation fails or CLI is missing.
func renderTypst(articleID int, source string) string {
	if !typst.Available() {
		return "<p><em>Typst 编译器未安装。</em></p>"
	}
	body, err := typst.CompileHTMLCached(articleID, source)
	if err != nil {
		log.Printf("[render] typst compile error: %v", err)
		return "<p><em>Typst 编译失败：" + err.Error() + "</em></p>"
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
		log.Printf("[render] typst line compile error: %v", err)
		return "<p><em>Typst 编译失败：" + err.Error() + "</em></p>"
	}
	return body
}

func init() {
	log.Println("[parsers] loaded markdown, wikidot, bbcode, html renderers")
}
