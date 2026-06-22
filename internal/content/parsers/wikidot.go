package parsers

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
)

// ── Wikidot regexes (matching PyKYCH exactly, same flags) ──────────────

var (
	reWDCode          = regexp.MustCompile(`(?is)\[\[code(?:\s+type\s*=\s*['"]([^'"]+)['"])?\]\](.*?)\[\[/code\]\]`)
	reWDDiv           = regexp.MustCompile(`(?is)\[\[div\s+class="([^"]*)"\]\](.*?)\[\[/div\]\]`)
	reWDTable         = regexp.MustCompile(`(?is)\[\[table\]\](.*?)\[\[/table\]\]`)
	reWDSpanClass     = regexp.MustCompile(`(?is)\[\[span\s+class="([^"]*)"\]\](.*?)\[\[/span\]\]`)
	reWDSpanStyle     = regexp.MustCompile(`(?is)\[\[span\s+style="([^"]*)"\]\](.*?)\[\[/span\]\]`)
	reWDCollapsible   = regexp.MustCompile(`(?is)\[\[collapsible\s+show="([^"]*)"\s+hide="([^"]*)"\]\](.*?)\[\[/collapsible\]\]`)
	reWDSize          = regexp.MustCompile(`(?is)\[\[size\s+([^\]]+)\]\](.*?)\[\[/size\]\]`)
	reWDColor         = regexp.MustCompile(`(?is)\[\[color\s+([^\]]+)\]\](.*?)\[\[/color\]\]`)
	reWDCenter        = regexp.MustCompile(`(?s)\[\[=\]\](.*?)\[\[/=\]\]`)
	reWDRight         = regexp.MustCompile(`(?s)\[\[>\]\](.*?)\[\[/>\]\]`)
	reWDJustify       = regexp.MustCompile(`(?s)\[\[==\]\](.*?)\[\[/==\]\]`)
	reWDSuperscript   = regexp.MustCompile(`\^\^(.+?)\^\^`)
	reWDSubscript     = regexp.MustCompile(`,,(.+?),,`)
	reWDLineBreak     = regexp.MustCompile(`(?i)\[\[br\]\]`)
	reWDAnchor        = regexp.MustCompile(`\[\[#\s+([^\]]+)\]\]`)
	reWDEscape        = regexp.MustCompile(`(?s)@@(.+?)@@`)
	reWDBold          = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reWDItalic        = regexp.MustCompile(`//(.+?)//`)
	reWDUnderline     = regexp.MustCompile(`__(.+?)__`)
	reWDStrikethrough = regexp.MustCompile(`--(.+?)--`)
	reWDInlineCode    = regexp.MustCompile(`\{\{(.+?)\}\}`)
	reWDWikiLink      = regexp.MustCompile(`\[\[\[([^\]]+?)(?:\s*\|\s*([^\]]+?))?\]\]\]`)
	reWDImage         = regexp.MustCompile(`(?i)\[\[image\s+([^\]]+?)\]\]`)
	reWDH4_           = regexp.MustCompile(`(?m)^\+\+\+\+\s+(.+)$`)
	reWDH3_           = regexp.MustCompile(`(?m)^\+\+\+\s+(.+)$`)
	reWDH2_           = regexp.MustCompile(`(?m)^\+\+\s+(.+)$`)
	reWDH1_           = regexp.MustCompile(`(?m)^\+\s+(.+)$`)
	reWDBlockquote    = regexp.MustCompile(`(?m)^(?:&gt;|>)\s?(.*)$`)
	reWDUnorderedItem = regexp.MustCompile(`(?m)^(\s*)\*\s+(.+)$`)
	reWDOrderedItem   = regexp.MustCompile(`(?m)^(\s*)#\s+(.+)$`)
	reWDHR            = regexp.MustCompile(`(?m)^-{4,}$`)
	reWDAdmonition    = regexp.MustCompile(`(?sm)^!!!\s+(note|warning|danger|info|tip)\s*\n(.*?)(\n!!!|\n\[\[|\z)`)
	reWDHTMLBlock     = regexp.MustCompile(`(?s)(<(?:pre|table|ul|ol|blockquote|div|details|summary)\b.*?</(?:pre|table|ul|ol|blockquote|div|details|summary)>)`)
)

// ── Size / color lookup tables (matching PyKYCH) ─────────────────────

var sizeMap = map[string]string{
	"xx-small":  "0.5rem", "x-small": "0.625rem", "smaller": "0.75rem",
	"small": "0.8rem", "medium": "1rem", "large": "1.25rem",
	"x-large": "1.5rem", "xx-large": "2rem", "larger": "2.5rem",
}

var colorNames = map[string]string{
	"red": "#e74c3c", "green": "#27ae60", "blue": "#3498db",
	"yellow": "#f1c40f", "orange": "#e67e22", "purple": "#9b59b6",
	"pink": "#e91e63", "gray": "#7f8c8d", "grey": "#7f8c8d",
	"black": "#2c3e50", "white": "#ecf0f1", "cyan": "#00bcd4",
	"teal": "#009688", "indigo": "#3f51b5",
}

var admonitionTitles = map[string]string{
	"note":    "📝 注意",
	"warning": "⚠️ 警告",
	"danger":  "🚫 危险",
	"info":    "ℹ️ 信息",
	"tip":     "💡 提示",
}

// RenderWikidot converts Wikidot markup source to HTML.
func RenderWikidot(source string) string {
	if source == "" {
		return ""
	}
	p := wpGet()
	defer wpPut(p)
	return p.convert(source)
}

// ── WikidotParser (singleton, pooled for thread safety) ──────────────

type wikidotParser struct {
	blocks  map[string]string
	counter int
}

var wpPool = sync.Pool{New: func() any { return &wikidotParser{} }}

func wpGet() *wikidotParser  { return wpPool.Get().(*wikidotParser) }
func wpPut(p *wikidotParser) { p.blocks = nil; p.counter = 0; wpPool.Put(p) }

func (p *wikidotParser) storeBlock(html string) string {
	p.counter++
	key := fmt.Sprintf("%%BLOCK_%d%%", p.counter)
	if p.blocks == nil {
		p.blocks = make(map[string]string)
	}
	p.blocks[key] = html
	return key
}

func (p *wikidotParser) convert(source string) string {
	out := source

	// Phase 1: Store complex blocks as placeholders.
	// 1a. Escape blocks
	out = reWDEscape.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDEscape.FindStringSubmatch(s)
		return p.storeBlock(html.EscapeString(m[1]))
	})
	// 1b. Code blocks
	out = reWDCode.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCode.FindStringSubmatch(s)
		return p.storeBlock(renderCodeBlock(m[2], m[1]))
	})
	// 1c. Collapsible
	out = reWDCollapsible.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCollapsible.FindStringSubmatch(s)
		inner := p.convert(m[3])
		return p.storeBlock(fmt.Sprintf(`<details class="wiki-collapsible"><summary>%s</summary><div class="collapsible-content">%s</div></details>`, m[1], inner))
	})
	// 1d. Tables
	out = reWDTable.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDTable.FindStringSubmatch(s)
		return p.storeBlock(renderWikidotTable(p, m[1]))
	})
	// 1e. Div containers
	out = reWDDiv.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDDiv.FindStringSubmatch(s)
		inner := p.convert(m[2])
		return p.storeBlock(fmt.Sprintf(`<div class="%s">%s</div>`, m[1], inner))
	})
	// 1f. Span class/style (recursive inline convert)
	out = reWDSpanClass.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSpanClass.FindStringSubmatch(s)
		inner := inlineOnly(p.convert(m[2]))
		return fmt.Sprintf(`<span class="%s">%s</span>`, m[1], inner)
	})
	out = reWDSpanStyle.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSpanStyle.FindStringSubmatch(s)
		inner := inlineOnly(p.convert(m[2]))
		return fmt.Sprintf(`<span style="%s">%s</span>`, m[1], inner)
	})
	// 1g. Size / Color
	out = reWDSize.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSize.FindStringSubmatch(s)
		css := m[1]
		if v, ok := sizeMap[strings.ToLower(css)]; ok {
			css = v
		}
		return fmt.Sprintf(`<span style="font-size:%s">%s</span>`, css, inlineOnly(m[2]))
	})
	out = reWDColor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDColor.FindStringSubmatch(s)
		css := m[1]
		if v, ok := colorNames[strings.ToLower(css)]; ok {
			css = v
		}
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, inlineOnly(m[2]))
	})
	// 1h. Alignment
	out = reWDCenter.ReplaceAllString(out, `<div style="text-align:center">$1</div>`)
	out = reWDRight.ReplaceAllString(out, `<div style="text-align:right">$1</div>`)
	out = reWDJustify.ReplaceAllString(out, `<div style="text-align:justify">$1</div>`)

	// Phase 2: Inline formatting (bold, italic, underline, strikethrough, super/sub, code)
	// Pre-process: replace backslash-escaped slashes with sentinels so // is not
	// confused with italic markers (Go regex lacks lookbehind).
	out = strings.ReplaceAll(out, `\\/`, "\x00SL")
	out = strings.ReplaceAll(out, `\\/`, "\x00SL") // doubled: `\\//` → `\x00SL\x00SL`

	out = reWDBold.ReplaceAllString(out, `<strong>$1</strong>`)
	out = reWDItalic.ReplaceAllString(out, `<em>$1</em>`)
	out = reWDUnderline.ReplaceAllString(out, `<u>$1</u>`)
	out = reWDStrikethrough.ReplaceAllString(out, `<s>$1</s>`)
	out = reWDSuperscript.ReplaceAllString(out, `<sup>$1</sup>`)
	out = reWDSubscript.ReplaceAllString(out, `<sub>$1</sub>`)
	out = reWDInlineCode.ReplaceAllString(out, `<code>$1</code>`)

	// Restore escaped slashes.
	out = strings.ReplaceAll(out, "\x00SL", "/")

	// Phase 3: Links and images
	out = reWDWikiLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		href := m[1]
		text := m[1]
		if m[2] != "" {
			text = m[2]
		}
		if !strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			href = "/wikidot/" + href
		}
		return fmt.Sprintf(`<a href="%s">%s</a>`, href, text)
	})
	out = reWDImage.ReplaceAllString(out, `<img src="$1" alt="" loading="lazy" style="max-width:100%">`)

	// Phase 4: Headings
	out = reWDH4_.ReplaceAllString(out, `<h4>$1</h4>`)
	out = reWDH3_.ReplaceAllString(out, `<h3>$1</h3>`)
	out = reWDH2_.ReplaceAllString(out, `<h2>$1</h2>`)
	out = reWDH1_.ReplaceAllString(out, `<h1>$1</h1>`)

	// Phase 5: Horizontal rules
	out = reWDHR.ReplaceAllString(out, `<hr>`)

	// Phase 6: Line breaks & anchors
	out = reWDLineBreak.ReplaceAllString(out, `<br>`)
	out = reWDAnchor.ReplaceAllString(out, `<span id="$1" class="wiki-anchor"></span>`)

	// Phase 7: Admonitions
	out = reWDAdmonition.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAdmonition.FindStringSubmatch(s)
		typ := m[1]
		content := strings.TrimSpace(p.convert(m[2]))
		title, ok := admonitionTitles[typ]
		if !ok {
			title = typ
		}
		return fmt.Sprintf(`<div class="admonition %s"><p class="admonition-title">%s</p>%s</div>`, typ, title, content)
	})

	// Phase 8: Blockquotes
	out = renderWikidotBlockquotes(out)

	// Phase 9: Lists
	out = renderWikidotLists(out)

	// Phase 10: Restore stored blocks
	for key, html := range p.blocks {
		out = strings.ReplaceAll(out, key, html)
	}

	// Phase 11: Paragraph wrapping
	out = wrapWikidotParagraphs(out)

	return out
}

// ── Helper renderers ──────────────────────────────────────────────────

func renderCodeBlock(code, lang string) string {
	c := html.EscapeString(code)
	cls := ""
	if lang != "" {
		cls = fmt.Sprintf(` class="language-%s"`, lang)
	}
	return fmt.Sprintf(`<pre><code%s>%s</code></pre>`, cls, c)
}

func renderWikidotTable(p *wikidotParser, raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	var sb strings.Builder
	sb.WriteString(`<table class="wiki-table"><tbody>`)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		tag := "td"
		if i == 0 {
			tag = "th"
		}
		sb.WriteString("<tr>")
		for _, cell := range strings.Split(line, "|") {
			c := strings.TrimSpace(cell)
			if i == 0 {
				c = p.convert(c)
			} else {
				c = inlineOnly(p.convert(c))
			}
			sb.WriteString(fmt.Sprintf("<%s>%s</%s>", tag, c, tag))
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</tbody></table>")
	return sb.String()
}

// inlineOnly applies inline formatting to text (no block elements).
func inlineOnly(text string) string {
	text = reWDBold.ReplaceAllString(text, `<strong>$1</strong>`)
	text = reWDItalic.ReplaceAllString(text, `<em>$1</em>`)
	text = reWDUnderline.ReplaceAllString(text, `<u>$1</u>`)
	text = reWDStrikethrough.ReplaceAllString(text, `<s>$1</s>`)
	text = reWDSuperscript.ReplaceAllString(text, `<sup>$1</sup>`)
	text = reWDSubscript.ReplaceAllString(text, `<sub>$1</sub>`)
	text = reWDInlineCode.ReplaceAllString(text, `<code>$1</code>`)
	text = reWDWikiLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		href := m[1]
		text := m[1]
		if m[2] != "" {
			text = m[2]
		}
		return fmt.Sprintf(`<a href="%s">%s</a>`, href, text)
	})
	return text
}

func renderWikidotBlockquotes(text string) string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			result = append(result, `<blockquote>`+strings.Join(buf, `<br />`)+`</blockquote>`)
			buf = nil
		}
	}
	for _, line := range lines {
		if m := reWDBlockquote.FindStringSubmatch(line); m != nil {
			buf = append(buf, m[1])
		} else {
			flush()
			result = append(result, line)
		}
	}
	flush()
	return strings.Join(result, "\n")
}

func renderWikidotLists(text string) string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	var buf []string
	var listType string // "ul" or "ol"
	flushList := func() {
		if len(buf) > 0 {
			result = append(result, "<"+listType+">"+strings.Join(buf, "")+"</"+listType+">")
			buf = nil
		}
	}
	for _, line := range lines {
		um := reWDUnorderedItem.FindStringSubmatch(line)
		om := reWDOrderedItem.FindStringSubmatch(line)
		if um != nil {
			if listType != "ul" {
				flushList()
				listType = "ul"
			}
			indent := len(um[1]) / 2
			prefix := strings.Repeat("  ", indent)
			buf = append(buf, prefix+"<li>"+inlineOnly(um[2])+"</li>")
		} else if om != nil {
			if listType != "ol" {
				flushList()
				listType = "ol"
			}
			indent := len(om[1]) / 2
			prefix := strings.Repeat("  ", indent)
			buf = append(buf, prefix+"<li>"+inlineOnly(om[2])+"</li>")
		} else {
			flushList()
			result = append(result, line)
		}
	}
	flushList()
	return strings.Join(result, "\n")
}

func wrapWikidotParagraphs(text string) string {
	// Protect HTML blocks (pre, table, ul, ol, blockquote, div, details, summary)
	blockIdx := 0
	blockStore := make(map[string]string)
	text = reWDHTMLBlock.ReplaceAllStringFunc(text, func(s string) string {
		key := fmt.Sprintf("%%WRAP_BLOCK_%d%%", blockIdx)
		blockIdx++
		blockStore[key] = s
		return key
	})

	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			result = append(result, "<p>"+strings.Join(buf, "<br />\n")+"</p>")
			buf = nil
		}
	}
	blockTagStart := regexp.MustCompile(`^<(h[1-6]|hr|li|p|img|blockquote|ul|ol|pre|table|div|details|summary)\b`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || blockTagStart.MatchString(trimmed) || strings.HasPrefix(trimmed, "%%WRAP_BLOCK_") {
			flush()
			result = append(result, line)
		} else {
			buf = append(buf, trimmed)
		}
	}
	flush()

	out := strings.Join(result, "\n")
	for key, html := range blockStore {
		out = strings.ReplaceAll(out, key, html)
	}
	return out
}
