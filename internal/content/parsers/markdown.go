package parsers

import (
	"bytes"
	"html"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

var (
	md     goldmark.Markdown // unsafe — for trusted article content (md type)
	mdSafe goldmark.Markdown // XSS-safe — for user-generated content (comments, notifications)
)

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.Typographer,
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(ghtml.WithUnsafe()),
	)
	// mdSafe omits WithUnsafe: any raw HTML in the source is escaped to text
	// instead of being emitted as live markup. This is the renderer to use
	// for anything user-authored (comments, line comments, notifications).
	mdSafe = goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.Typographer,
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
}

// RenderMarkdown converts Markdown source to HTML via Goldmark. It uses the
// "unsafe" renderer that allows raw HTML in the source — only use this for
// trusted article content authored by admins.
func RenderMarkdown(source string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return "<p>" + html.EscapeString(source) + "</p>"
	}
	return buf.String()
}

// RenderSafeMarkdown is the XSS-safe variant of RenderMarkdown. Raw HTML in
// the source is escaped to text. Use this for user-generated content:
// comments, line comments, notifications.
func RenderSafeMarkdown(source string) string {
	var buf bytes.Buffer
	if err := mdSafe.Convert([]byte(source), &buf); err != nil {
		return "<p>" + html.EscapeString(source) + "</p>"
	}
	return buf.String()
}
