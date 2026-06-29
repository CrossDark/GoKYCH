package parsers

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

// ── XSS guards (defense-in-depth on top of html.EscapeString) ────────
//
// html.EscapeString at the top of RenderBBCode catches `<` and `>`, but it
// leaves URL schemes like `javascript:` and attribute-breaking characters in
// user-supplied href/src/style values untouched. Without these guards, BBCode
// like `[url=javascript:alert(1)]click[/url]` would inject a live `onclick`
// into the rendered HTML.
//
// sanitizeURLForAttr is used for every href / src / cite value. It drops
// anything that isn't:
//   - a same-site path (must start with "/", but not "//" or "/\"), OR
//   - a fragment-only anchor ("#…") used for in-page jump buttons, OR
//   - a URL whose scheme is in the allowlist (http / https / mailto).
//
// Returns "" on rejection so callers can omit the attribute entirely.
//
// Any character that could break out of an HTML attribute (quote, angle
// bracket, backtick, control char) is rejected BEFORE the path/scheme
// branches, so a payload like `/wikidot/" onmouseover=...` (where a Wikidot
// link expands to a path that contains a literal quote) is dropped instead
// of being treated as a valid internal link.
func sanitizeURLForAttr(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if r == '"' || r == '\'' || r == '<' || r == '>' || r == '`' || r < 0x20 || r == 0x7f {
			return ""
		}
	}
	// Fragment-only anchor (`#section-id` or bare `#`) — used by
	// `[[button Label]]` placeholder buttons. Reject anything
	// that LOOKS like a scheme (e.g. `#javascript:`) by parsing
	// it; a bare fragment has no scheme so the parse returns
	// Scheme == "". Allow only when no scheme is present.
	if raw[0] == '#' {
		if u, err := url.Parse(raw); err == nil && u.Scheme == "" {
			return raw
		}
		return ""
	}
	// Same-site path. Reject protocol-relative ("//evil.com") and backslash
	// tricks ("/\evil.com") that some browsers normalise into a host change.
	if raw[0] == '/' {
		if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
			return ""
		}
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return raw
	}
	return ""
}

// sanitizeCSSValue guards inline style values for BBCode [size]/[color]/
// [font]/[bg] and Wikidot [[span style=…]]/[[div style=…]]. Rejects
// anything that:
//   - contains CSS metacharacters that allow rule breaks or function
//     calls ( { } ( ) ),
//   - contains "expression" or "javascript:" / "url(" which enable IE-style
//     or modern CSS-injection attacks,
//   - or contains characters outside [A-Za-z0-9_#.,%/\- : ] (e.g. quotes
//     that would let an attacker escape the style attribute).
//
// Semicolons ( ; ) are stripped before the blocklist check: they're not
// dangerous on their own (just declaration separators), and rejecting
// them outright forced users to write `background: yellow` without the
// trailing `;` that any CSS reference or copy-pasted example includes.
// A payload like `color:red; background:url(javascript:alert(1))` still
// trips the url() blocklist after the `;` collapses to a space — see
// sanitize_test.go's rejection cases for the full coverage.
func sanitizeCSSValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Strip declaration separators before the content checks. The output
	// keeps the original `;` (we only normalise for validation).
	sanitised := strings.ReplaceAll(raw, ";", " ")
	lower := strings.ToLower(sanitised)
	for _, bad := range []string{"{", "}", "(", ")", "expression", "javascript:", "url(", "@import"} {
		if strings.Contains(lower, bad) {
			return ""
		}
	}
	for _, r := range sanitised {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '#' || r == '.' || r == ',' ||
			r == '%' || r == '/' || r == '-' || r == ' ' || r == ':') {
			return ""
		}
	}
	return raw
}

// sanitizeAnchorID allows only [A-Za-z0-9_-], so an admin can't break out
// of the id attribute with a quote.
func sanitizeAnchorID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return ""
		}
	}
	return raw
}

// BBCode regex patterns (order-sensitive, matching PyKYCH pipeline).
var (
	reCode     = regexp.MustCompile(`(?is)\[code(?:=(\w+))?\](.*?)\[/code\]`)
	reQuote    = regexp.MustCompile(`(?is)\[quote(?:=(.*?))?\](.*?)\[/quote\]`)
	reSpoiler  = regexp.MustCompile(`(?is)\[spoiler(?:=(.*?))?\](.*?)\[/spoiler\]`)
	reTable    = regexp.MustCompile(`(?is)\[table\](.*?)\[/table\]`)
	reHR       = regexp.MustCompile(`(?i)\[hr\]`)
	reCenter   = regexp.MustCompile(`(?is)\[center\](.*?)\[/center\]`)
	reRight    = regexp.MustCompile(`(?is)\[right\](.*?)\[/right\]`)
	reLeft     = regexp.MustCompile(`(?is)\[left\](.*?)\[/left\]`)
	reBold     = regexp.MustCompile(`(?is)\[b\](.*?)\[/b\]`)
	reItalic   = regexp.MustCompile(`(?is)\[i\](.*?)\[/i\]`)
	reUnder    = regexp.MustCompile(`(?is)\[u\](.*?)\[/u\]`)
	reStrike   = regexp.MustCompile(`(?is)\[s\](.*?)\[/s\]`)
	reSup      = regexp.MustCompile(`(?is)\[sup\](.*?)\[/sup\]`)
	reSub      = regexp.MustCompile(`(?is)\[sub\](.*?)\[/sub\]`)
	reURLText  = regexp.MustCompile(`(?is)\[url=([^\]]+)\](.*?)\[/url\]`)
	reURL      = regexp.MustCompile(`(?is)\[url\](.*?)\[/url\]`)
	reEmail    = regexp.MustCompile(`(?is)\[email\](.*?)\[/email\]`)
	reImg      = regexp.MustCompile(`(?is)\[img\](.*?)\[/img\]`)
	reSize     = regexp.MustCompile(`(?is)\[size=([^\]]+)\](.*?)\[/size\]`)
	reColor    = regexp.MustCompile(`(?is)\[color=([^\]]+)\](.*?)\[/color\]`)
	reFont     = regexp.MustCompile(`(?is)\[font=([^\]]+)\](.*?)\[/font\]`)
	reBG       = regexp.MustCompile(`(?is)\[bg=([^\]]+)\](.*?)\[/bg\]`)
	reVideo    = regexp.MustCompile(`(?is)\[video\](.*?)\[/video\]`)
	reAudio    = regexp.MustCompile(`(?is)\[audio\](.*?)\[/audio\]`)
	reAnchor   = regexp.MustCompile(`(?is)\[anchor\](.*?)\[/anchor\]`)
	reList     = regexp.MustCompile(`(?is)\[list(?:=(1))?\](.*?)\[/list\]`)
	reEscLeft  = regexp.MustCompile(`\\\[`)
	reEscRight = regexp.MustCompile(`\\\]`)
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

	// URL-bearing tags: route every value through sanitizeURLForAttr so
	// javascript:/data:/vbscript: payloads are dropped (they'd otherwise
	// survive html.EscapeString and execute on click).
	out = reURLText.ReplaceAllStringFunc(out, func(s string) string {
		m := reURLText.FindStringSubmatch(s)
		if safe := sanitizeURLForAttr(m[1]); safe != "" {
			return `<a href="` + safe + `" target="_blank" rel="noopener noreferrer">` + m[2] + `</a>`
		}
		return m[2]
	})
	out = reURL.ReplaceAllStringFunc(out, func(s string) string {
		m := reURL.FindStringSubmatch(s)
		if safe := sanitizeURLForAttr(m[1]); safe != "" {
			return `<a href="` + safe + `" target="_blank" rel="noopener noreferrer">` + safe + `</a>`
		}
		return ""
	})
	out = reEmail.ReplaceAllStringFunc(out, func(s string) string {
		m := reEmail.FindStringSubmatch(s)
		if safe := sanitizeURLForAttr("mailto:" + m[1]); safe != "" {
			return `<a href="` + safe + `">` + m[1] + `</a>`
		}
		return m[1]
	})
	out = reImg.ReplaceAllStringFunc(out, func(s string) string {
		m := reImg.FindStringSubmatch(s)
		if safe := sanitizeURLForAttr(m[1]); safe != "" {
			return `<img src="` + safe + `" alt="" loading="lazy" style="max-width:100%">`
		}
		return ""
	})

	// Style-bearing tags: known tokens (sizeMap / colorNames) pass through
	// verbatim; everything else is filtered through sanitizeCSSValue which
	// only permits alphanumerics + a small set of CSS-safe punctuation.
	out = reSize.ReplaceAllStringFunc(out, func(s string) string {
		m := reSize.FindStringSubmatch(s)
		css := m[1]
		if v, ok := sizeMap[strings.ToLower(css)]; ok {
			css = v
		} else if css = sanitizeCSSValue(css); css == "" {
			return m[2]
		}
		return `<span style="font-size:` + css + `">` + m[2] + `</span>`
	})
	out = reColor.ReplaceAllStringFunc(out, func(s string) string {
		m := reColor.FindStringSubmatch(s)
		css := m[1]
		if v, ok := colorNames[strings.ToLower(css)]; ok {
			css = v
		} else if css = sanitizeCSSValue(css); css == "" {
			return m[2]
		}
		return `<span style="color:` + css + `">` + m[2] + `</span>`
	})
	out = reFont.ReplaceAllStringFunc(out, func(s string) string {
		m := reFont.FindStringSubmatch(s)
		if css := sanitizeCSSValue(m[1]); css != "" {
			return `<span style="font-family:` + css + `">` + m[2] + `</span>`
		}
		return m[2]
	})
	out = reBG.ReplaceAllStringFunc(out, func(s string) string {
		m := reBG.FindStringSubmatch(s)
		if css := sanitizeCSSValue(m[1]); css != "" {
			return `<span style="background-color:` + css + `">` + m[2] + `</span>`
		}
		return m[2]
	})

	out = reVideo.ReplaceAllStringFunc(out, func(s string) string {
		m := reVideo.FindStringSubmatch(s)
		if safe := sanitizeURLForAttr(m[1]); safe != "" {
			return `<video controls style="max-width:100%"><source src="` + safe + `"></video>`
		}
		return ""
	})
	out = reAudio.ReplaceAllStringFunc(out, func(s string) string {
		m := reAudio.FindStringSubmatch(s)
		if safe := sanitizeURLForAttr(m[1]); safe != "" {
			return `<audio controls><source src="` + safe + `"></audio>`
		}
		return ""
	})
	out = reAnchor.ReplaceAllStringFunc(out, func(s string) string {
		m := reAnchor.FindStringSubmatch(s)
		if safe := sanitizeAnchorID(m[1]); safe != "" {
			return `<span id="` + safe + `" class="bbcode-anchor"></span>`
		}
		return ""
	})

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
