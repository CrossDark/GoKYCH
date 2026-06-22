package parsers

import (
	"html"
	"regexp"
	"strings"
)

// BBCode regex patterns (order-sensitive, matching PyKYCH pipeline).
var (
	reCode    = regexp.MustCompile(`(?is)\[code(?:=(\w+))?\](.*?)\[/code\]`)
	reQuote   = regexp.MustCompile(`(?is)\[quote(?:=(.*?))?\](.*?)\[/quote\]`)
	reSpoiler = regexp.MustCompile(`(?is)\[spoiler(?:=(.*?))?\](.*?)\[/spoiler\]`)
	reTable   = regexp.MustCompile(`(?is)\[table\](.*?)\[/table\]`)
	reHR      = regexp.MustCompile(`(?i)\[hr\]`)
	reCenter  = regexp.MustCompile(`(?is)\[center\](.*?)\[/center\]`)
	reRight   = regexp.MustCompile(`(?is)\[right\](.*?)\[/right\]`)
	reLeft    = regexp.MustCompile(`(?is)\[left\](.*?)\[/left\]`)
	reBold    = regexp.MustCompile(`(?is)\[b\](.*?)\[/b\]`)
	reItalic  = regexp.MustCompile(`(?is)\[i\](.*?)\[/i\]`)
	reUnder   = regexp.MustCompile(`(?is)\[u\](.*?)\[/u\]`)
	reStrike  = regexp.MustCompile(`(?is)\[s\](.*?)\[/s\]`)
	reSup     = regexp.MustCompile(`(?is)\[sup\](.*?)\[/sup\]`)
	reSub     = regexp.MustCompile(`(?is)\[sub\](.*?)\[/sub\]`)
	reURLText = regexp.MustCompile(`(?is)\[url=([^\]]+)\](.*?)\[/url\]`)
	reURL     = regexp.MustCompile(`(?is)\[url\](.*?)\[/url\]`)
	reEmail   = regexp.MustCompile(`(?is)\[email\](.*?)\[/email\]`)
	reImg     = regexp.MustCompile(`(?is)\[img\](.*?)\[/img\]`)
	reSize    = regexp.MustCompile(`(?is)\[size=([^\]]+)\](.*?)\[/size\]`)
	reColor   = regexp.MustCompile(`(?is)\[color=([^\]]+)\](.*?)\[/color\]`)
	reFont    = regexp.MustCompile(`(?is)\[font=([^\]]+)\](.*?)\[/font\]`)
	reBG      = regexp.MustCompile(`(?is)\[bg=([^\]]+)\](.*?)\[/bg\]`)
	reVideo   = regexp.MustCompile(`(?is)\[video\](.*?)\[/video\]`)
	reAudio   = regexp.MustCompile(`(?is)\[audio\](.*?)\[/audio\]`)
	reAnchor  = regexp.MustCompile(`(?is)\[anchor\](.*?)\[/anchor\]`)
	reList    = regexp.MustCompile(`(?is)\[list(?:=(1))?\](.*?)\[/list\]`)
	reEscLeft = regexp.MustCompile(`\\\[`)
	reEscRight= regexp.MustCompile(`\\\]`)
)

// RenderBBCode converts BBCode source to HTML.
func RenderBBCode(text string) string {
	if text == "" {
		return ""
	}
	out := html.EscapeString(text)

	// Block-level first (before inline formatting).
	out = reCode.ReplaceAllStringFunc(out, func(s string) string {
		m := reCode.FindStringSubmatch(s)
		return renderCode(m[2], m[1])
	})
	out = reQuote.ReplaceAllStringFunc(out, func(s string) string {
		m := reQuote.FindStringSubmatch(s)
		return renderQuote(m[2], m[1])
	})
	out = reSpoiler.ReplaceAllStringFunc(out, func(s string) string {
		m := reSpoiler.FindStringSubmatch(s)
		return renderSpoiler(m[2], m[1])
	})
	out = parseBBCodeLists(out)
	out = reTable.ReplaceAllStringFunc(out, func(s string) string {
		m := reTable.FindStringSubmatch(s)
		return renderBBCodeTable(m[1])
	})
	out = reHR.ReplaceAllString(out, "<hr>")
	out = reCenter.ReplaceAllString(out, `<div style="text-align:center">$1</div>`)
	out = reRight.ReplaceAllString(out, `<div style="text-align:right">$1</div>`)
	out = reLeft.ReplaceAllString(out, `<div style="text-align:left">$1</div>`)

	// Inline formatting.
	out = reBold.ReplaceAllString(out, `<strong>$1</strong>`)
	out = reItalic.ReplaceAllString(out, `<em>$1</em>`)
	out = reUnder.ReplaceAllString(out, `<u>$1</u>`)
	out = reStrike.ReplaceAllString(out, `<s>$1</s>`)
	out = reSup.ReplaceAllString(out, `<sup>$1</sup>`)
	out = reSub.ReplaceAllString(out, `<sub>$1</sub>`)
	out = reURLText.ReplaceAllString(out, `<a href="$1" target="_blank" rel="noopener noreferrer">$2</a>`)
	out = reURL.ReplaceAllString(out, `<a href="$1" target="_blank" rel="noopener noreferrer">$1</a>`)
	out = reEmail.ReplaceAllString(out, `<a href="mailto:$1">$1</a>`)
	out = reImg.ReplaceAllString(out, `<img src="$1" alt="" loading="lazy" style="max-width:100%">`)
	out = reSize.ReplaceAllString(out, `<span style="font-size:$1">$2</span>`)
	out = reColor.ReplaceAllString(out, `<span style="color:$1">$2</span>`)
	out = reFont.ReplaceAllString(out, `<span style="font-family:$1">$2</span>`)
	out = reBG.ReplaceAllString(out, `<span style="background-color:$1">$2</span>`)
	out = reVideo.ReplaceAllString(out, `<video controls style="max-width:100%"><source src="$1"></video>`)
	out = reAudio.ReplaceAllString(out, `<audio controls><source src="$1"></audio>`)
	out = reAnchor.ReplaceAllString(out, `<span id="$1" class="bbcode-anchor"></span>`)

	// Unescape literal brackets.
	out = reEscLeft.ReplaceAllString(out, "[")
	out = reEscRight.ReplaceAllString(out, "]")

	return out
}

func renderCode(code, lang string) string {
	c := strings.TrimSpace(code)
	if lang != "" {
		return `<pre><code class="language-` + lang + `">` + c + `</code></pre>`
	}
	return `<pre><code>` + c + `</code></pre>`
}

func renderQuote(text, author string) string {
	t := strings.TrimSpace(text)
	cite := ""
	if author != "" {
		cite = `<cite>` + author + `</cite>`
	}
	return `<blockquote class="bbcode-quote">` + cite + t + `</blockquote>`
}

func renderSpoiler(text, title string) string {
	t := strings.TrimSpace(text)
	label := "Spoiler"
	if title != "" {
		label = title
	}
	return `<details class="bbcode-spoiler"><summary>` + label + `</summary><div class="bbcode-spoiler-content">` + t + `</div></details>`
}

func renderBBCodeTable(content string) string {
	rows := strings.Split(content, "[/tr]")
	var sb strings.Builder
	sb.WriteString(`<div class="bbcode-table-wrapper"><table class="bbcode-table">`)
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		sb.WriteString("<tr>")
		// Match [td]...[td] or [th]...[th]
		cellRe := regexp.MustCompile(`(?is)\[(td|th)\](.*?)\[/\1\]`)
		cells := cellRe.FindAllStringSubmatch(row, -1)
		for _, cell := range cells {
			sb.WriteString("<" + cell[1] + ">" + cell[2] + "</" + cell[1] + ">")
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</table></div>")
	return sb.String()
}

func parseBBCodeLists(text string) string {
	re := reList
	for re.MatchString(text) {
		text = re.ReplaceAllStringFunc(text, func(s string) string {
			m := re.FindStringSubmatch(s)
			tag := "ul"
			cls := "bbcode-list"
			if m[1] == "1" {
				tag = "ol"
			}
			inner := m[2]
			// Split items
			parts := regexp.MustCompile(`\[\/\*\]`).Split(inner, -1)
			if len(parts) <= 1 {
				parts = regexp.MustCompile(`\[\*\]`).Split(inner, -1)
			}
			items := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				p = strings.TrimPrefix(p, "[*]")
				p = strings.TrimSpace(p)
				if p != "" {
					items = append(items, "<li>"+p+"</li>")
				}
			}
			return "<" + tag + ` class="` + cls + `">` + strings.Join(items, "") + "</" + tag + ">"
		})
	}
	return text
}
