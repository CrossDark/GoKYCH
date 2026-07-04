package parsers

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
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
	reH1       = regexp.MustCompile(`(?is)\[h1\](.*?)\[/h1\]`)
	reH2       = regexp.MustCompile(`(?is)\[h2\](.*?)\[/h2\]`)
	reH3       = regexp.MustCompile(`(?is)\[h3\](.*?)\[/h3\]`)
	reH4       = regexp.MustCompile(`(?is)\[h4\](.*?)\[/h4\]`)
	reH5       = regexp.MustCompile(`(?is)\[h5\](.*?)\[/h5\]`)
	reH6       = regexp.MustCompile(`(?is)\[h6\](.*?)\[/h6\]`)
	rePlainNum = regexp.MustCompile(`^\d+(\.\d+)?$`)
	reEscLeft  = regexp.MustCompile(`\\\[`)
	reEscRight = regexp.MustCompile(`\\\]`)
)

// sizeScaleMap maps the classic 1-7 HTML font scale (BBCode `[size=N]`
// for N in 1..7) to rem values, so `[size=7]` renders huge instead of an
// invalid unitless `font-size:7`.
var sizeScaleMap = map[string]string{
	"1": "0.5rem", "2": "0.75rem", "3": "1rem",
	"4": "1.25rem", "5": "1.5rem", "6": "1.75rem", "7": "2rem",
}

// reBBSizeOpen matches only the opener portion of `[size=…]…[/size]`,
// separate from reSize so the depth-counter loop below doesn't confuse
// the opener end with a close.
var reBBSizeOpen = regexp.MustCompile(`(?is)\[size=[^\]]+\]`)

// extractBBCodeSizeBody scans `text` starting at `from` (just past a
// `[size=…]` opener) for the matching `[/size]`, accounting for nested
// `[size=…]` openers. Returns the inner body, ok=true on success, and
// the index immediately after the close tag (caller resumes from
// there). Returns ok=false when no close is found.
func extractBBCodeSizeBody(text string, from int) (string, bool, int) {
	const close = "[/size]"
	j := from
	depth := 1
	for j < len(text) {
		nextOpen := reBBSizeOpen.FindStringIndex(text[j:])
		nextClose := strings.Index(text[j:], close)
		switch {
		case nextClose < 0:
			return "", false, 0
		case nextOpen != nil && (j+nextOpen[0]) < (j+nextClose):
			depth++
			j = j + nextOpen[1]
		default:
			depth--
			closeAt := j + nextClose
			closeEnd := closeAt + len(close)
			if depth == 0 {
				return text[from:closeAt], true, closeEnd
			}
			j = closeEnd
		}
	}
	return "", false, 0
}

// reBBSizeExplicitUnit matches a number with an explicit CSS unit
// (`12pt`, `0.8em`, `24px`, `80%`, `1.25rem`). Used as a whitelist
// for non-keyword size values: anything outside this shape (e.g.
// `[size=giant]`, a made-up keyword the author probably typo'd) is
// rejected so it doesn't sneak through sanitizeCSSValue's loose
// alphanumerics-only filter and render as `font-size:giant`.
//
// Mirrors Wikidot's `reWDSizeValue` whitelist
// (`^\d+(\.\d+)?(px|em|%)?$`), extended with `pt` and `rem` so the
// common BBCode usages keep working.
var reBBSizeExplicitUnit = regexp.MustCompile(`(?i)^\d+(?:\.\d+)?(?:px|pt|em|rem|%)$`)

// resolveBBCodeSize turns a `[size=X]` value into a valid CSS font-size.
// Accepted forms:
//  1. keyword in sizeMap (small/large/medium/…) → rem.
//  2. plain integer 1-7 → HTML font scale (sizeScaleMap, rem).
//  3. plain number >7: ≤40 → Npx (e.g. [size=14] → 14px); >40 → N%
//     (e.g. [size=150] → 150%, the phpBB percentage convention).
//  4. explicit unit (Npx/Nem/Nrem/N%/Npt) → validated via sanitizeCSSValue.
// Returns "" when the value can't be made safe (caller drops the wrapper
// rather than emitting invalid CSS like `font-size:150`).
func resolveBBCodeSize(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if v, ok := sizeMap[strings.ToLower(token)]; ok {
		return v
	}
	if rePlainNum.MatchString(token) {
		if v, ok := sizeScaleMap[token]; ok {
			return v
		}
		f, _ := strconv.ParseFloat(token, 64)
		if f > 40 {
			return token + "%"
		}
		return token + "px"
	}
	if reBBSizeExplicitUnit.MatchString(token) {
		if css := sanitizeCSSValue(token); css != "" {
			return css
		}
	}
	return ""
}

// renderBBCodeSizeBlocks walks `text` left-to-right, matching
// `[size=…]…[/size]` blocks via a depth counter so a nested
// `[size=…]` inside the inner body doesn't trip the outer close.
// The body itself is recursively processed so nested size blocks
// become nested `<span style="font-size:…">` wrappers.
//
// Per [size=N] value handling is delegated to resolveBBCodeSize:
// keyword → rem, `1`-`7` → HTML font scale (rem), bare number ≤40
// → Npx, bare number >40 → N% (phpBB convention), explicit unit
// (e.g. `0.8em`, `12pt`) → as-is after sanitizeCSSValue. Invalid
// values (e.g. `giant`, CSS injection payloads) fall through to the
// inner text so the author sees the broken construct instead of an
// invisible wrapper.
//
// Mirrors renderWikidotSizeBlocks but for the `[size=N]…[/size]`
// BBCode syntax (vs Wikidot's `[[size N]]…[[/size]]`).
func renderBBCodeSizeBlocks(text string) string {
	const close = "[/size]"
	var sb strings.Builder
	i := 0
	for i < len(text) {
		loc := reBBSizeOpen.FindStringIndex(text[i:])
		if loc == nil {
			sb.WriteString(text[i:])
			return sb.String()
		}
		openerStart := i + loc[0]
		openerEnd := i + loc[1]
		// Emit prefix verbatim.
		sb.WriteString(text[i:openerStart])
		// Extract the size value between `[size=` and `]`.
		m := reBBSizeOpen.FindStringSubmatch(text[openerStart:])
		if m == nil {
			sb.WriteString(text[openerStart:openerEnd])
			i = openerEnd
			continue
		}
		css := resolveBBCodeSize(strings.TrimSuffix(strings.TrimPrefix(m[0], "[size="), "]"))
		body, ok, next := extractBBCodeSizeBody(text, openerEnd)
		if !ok {
			// No matching close — leave the opener raw.
			sb.WriteString(text[openerStart:])
			return sb.String()
		}
		if css == "" {
			// Unrecognised size value (typo or sanitiser-rejected CSS
			// injection like `[size=12px; background:url(javascript:…)]`).
			// Wikidot's `[[size bad]]x[[/size]]` collapses the whole
			// construct down to `x`; we do the same — drop the wrapper
			// and re-emit only the inner body (recursively so any
			// nested valid `[size=…]` still renders).
			sb.WriteString(renderBBCodeSizeBlocks(body))
		} else {
			bodyRendered := renderBBCodeSizeBlocks(body)
			sb.WriteString(`<span style="font-size:`)
			sb.WriteString(css)
			sb.WriteString(`">`)
			sb.WriteString(bodyRendered)
			sb.WriteString(`</span>`)
		}
		i = next
	}
	return sb.String()
}

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

	// Headings (block-level). Inline formatting inside is still processed
	// by the inline passes below, so `[h1][b]x[/b][/h1]` → <h1><strong>x</strong></h1>.
	out = reH1.ReplaceAllString(out, "<h1>$1</h1>")
	out = reH2.ReplaceAllString(out, "<h2>$1</h2>")
	out = reH3.ReplaceAllString(out, "<h3>$1</h3>")
	out = reH4.ReplaceAllString(out, "<h4>$1</h4>")
	out = reH5.ReplaceAllString(out, "<h5>$1</h5>")
	out = reH6.ReplaceAllString(out, "<h6>$1</h6>")

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
	// Style-bearing tags: known tokens (sizeMap / colorNames) pass through
	// verbatim; everything else is filtered through sanitizeCSSValue which
	// only permits alphanumerics + a small set of CSS-safe punctuation.
	//
	// `[size=N]` has a richer grammar than `[color]`/[font]/[bg]: beyond
	// the keyword table we accept `1`-`7` (HTML font scale, rem) and a
	// bare number with implicit `px` (≤40) or `%` (>40, phpBB convention).
	// resolveBBCodeSize encodes that table; falling back to it makes
	// `[size=14]` render at 14px and `[size=150]` at 150% instead of
	// producing unitless `font-size:14` / `font-size:150` (which the
	// browser silently ignores — the whole reason this branch used to
	// look "broken"). We also re-run the replace from the head of the
	// emitted span so a nested `[size=…]` inside the inner text is
	// handled by the same logic on the second pass.
	out = renderBBCodeSizeBlocks(out)
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
	// RE2 does not support backreferences (\1), so we match either
	// open tag with a non-backreferencing close, using the opening
	// tag name for output. Mismatched open/close (e.g. [td]...[/th])
	// is an authoring error we tolerate gracefully.
	cellRe := regexp.MustCompile(`(?is)\[(td|th)\](.*?)\[/(?:td|th)\]`)
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		sb.WriteString("<tr>")
		for _, m := range cellRe.FindAllStringSubmatch(row, -1) {
			sb.WriteString("<" + m[1] + ">" + m[2] + "</" + m[1] + ">")
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
