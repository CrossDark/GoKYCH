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

// Render converts raw source of the given type into safe HTML.
func Render(at ArticleType, source string) template.HTML {
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
		html = renderTypst(source)
	default:
		html = "<p>不支持的格式。</p>"
	}
	return template.HTML(html)
}

// RenderLine breaks a multi-line source into lines that match the original
// source line numbering (for line comments). It returns rendered HTML for
// the full source plus a per-line HTML slice. Each line is individually
// rendered (important for inline-only contexts in wikidot/bbcode).
func RenderLines(at ArticleType, source string) (full template.HTML, lines []template.HTML) {
	full = Render(at, source)
	rawLines := strings.Split(source, "\n")
	lines = make([]template.HTML, len(rawLines))
	for i, l := range rawLines {
		lines[i] = Render(at, l)
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

// renderTypst compiles typst source to HTML via the typst CLI.
// Falls back to a placeholder if compilation fails or CLI is missing.
func renderTypst(source string) string {
	if !typst.Available() {
		return "<p><em>Typst 编译器未安装。</em></p>"
	}
	body, err := typst.CompileHTML(source)
	if err != nil {
		log.Printf("[render] typst compile error: %v", err)
		return "<p><em>Typst 编译失败：" + err.Error() + "</em></p>"
	}
	return body
}

func init() {
	log.Println("[parsers] loaded markdown, wikidot, bbcode, html renderers")
}
