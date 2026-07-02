package parsers

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

func renderEmailLink(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at >= len(addr)-1 {
		return html.EscapeString(addr)
	}
	user := addr[:at]
	domain := addr[at+1:]
	display := user + "@" + domain
	return fmt.Sprintf(
		`<a class="wikidot-email" href="mailto:%s" data-user="%s" data-domain="%s">%s</a>`,
		html.EscapeString(display),
		html.EscapeString(user),
		html.EscapeString(domain),
		html.EscapeString(display),
	)
}

// emitHeadingUnified is the single-regex successor to the
// level-by-level `emitHeading` / `emitHeadingStar` pair. The
// heading regex `reWDHeading` captures the plus-prefix as a
// single token (1-6 `+`s, optionally followed by `*` for
// SkipTOC), so the level is just `len(prefix)` and SkipTOC is
// `prefix[len-1] == '*'`. Walking the source in a single
// regex pass (rather than one pass per level) preserves the
// source-order of headings in `p.headings`, which is what the
// TOC builder and the `[[[page#h2-N]]]` / `[#name text]`
// cross-reference helpers both assume.
func inlineOnlyProse(s string) string {
	return inlineOnly(s)
}

// renderInputTag turns `[[input …]]` into `<input … />`. The
// `type` defaults to `text` when omitted (matches HTML
// forms' default). Unknown attributes (e.g. `placeholder=`,
// `pattern=`, `required`) are passed through after
// sanitisation so authors can wire up the full HTML5 input
// surface without wikidot having to learn every new key.
func inlineOnly(text string) string {
	// Apply %%var%% substitution against the current render's
	// shadow table (set by the calling wikidotParser.convert()).
	text = reWDVar.ReplaceAllStringFunc(text, func(s string) string {
		inlineVarsMu.Lock()
		vars := inlineVars
		inlineVarsMu.Unlock()
		if len(vars) == 0 {
			return s
		}
		m := reWDVar.FindStringSubmatch(s)
		if v, ok := vars[m[1]]; ok {
			return html.EscapeString(v)
		}
		return s
	})
	text = reWDBold.ReplaceAllString(text, `<strong>$1</strong>`)
	// See note on the package-level reWDItalic — same fix applied here
	// so a Phase-9 list / Phase-1g span / Phase-1e table call back into
	// inlineOnly on text containing already-wrapped auto-link URLs
	// doesn't insert a stray <em> in the middle of an <a href>.
	text = reWDItalic.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDItalic.FindStringSubmatch(s)
		return m[1] + "<em>" + m[2] + "</em>"
	})
	text = reWDUnderline.ReplaceAllString(text, `<u>$1</u>`)
	text = reWDStrikethrough.ReplaceAllString(text, `<s>$1</s>`)
	text = reWDSuperscript.ReplaceAllString(text, `<sup>$1</sup>`)
	text = reWDSubscript.ReplaceAllString(text, `<sub>$1</sub>`)
	text = reWDInlineCode.ReplaceAllString(text, `<code>$1</code>`)
	text = reWDInlineColor.ReplaceAllStringFunc(text, applyWikidotInlineColor)
	// Inline footnote refs in list items / table cells.
	text = reWDFootnoteRef.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDFootnoteRef.FindStringSubmatch(s)
		// No parser-context access from inlineOnly — we
		// conservatively render all numeric `[N]` as a generic
		// footnote-ref back-link. The parser's main pass will
		// already have replaced body refs with the real id;
		// this is just defensive.
		n := m[1]
		return fmt.Sprintf(`<sup class="footnote-ref"><a href="#fn-%s">%s</a></sup>`, n, html.EscapeString(n))
	})
	text = reWDExternalLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDExternalLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		display := strings.TrimSpace(m[2])
		if display == "" {
			display = url
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, html.EscapeString(display))
		}
		return html.EscapeString(display)
	})
	text = reWDMailto.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDMailto.FindStringSubmatch(s)
		addr := strings.TrimSpace(m[1])
		display := strings.TrimSpace(m[2])
		if display == "" {
			display = addr
		}
		if safe := sanitizeURLForAttr("mailto:" + addr); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(display))
		}
		return html.EscapeString(display)
	})
	text = reWDStarredTripleLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDStarredTripleLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		alias := strings.TrimSpace(m[2])
		return processWikidotLink("*"+url, alias)
	})
	text = reWDWikiLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		target := strings.TrimSpace(m[1])
		alias := strings.TrimSpace(m[2])
		return processWikidotLink(target, alias)
	})
	return text
}

// renderWikidotHeadingInline renders inline wikidot markup inside a
// heading's captured Text so the TOC builder (Phase 12) sees proper
// HTML instead of raw wikidot source.
//
// Heading content is always inline by definition (a heading can't
// contain a paragraph, list, div, table, etc.), so we run a subset
// of the inline passes: basic formatting (bold/italic/underline/
// strikethrough/sup/sub/code/color), links (external, mailto,
// triple-bracket wiki links including anchor + auto-slugify),
// [[size …]]…[[/size]], and balanced [[span …]]…[[/span]] (ruby/
// rt/rb/keycap + generic class/style/data spans).
//
// We deliberately skip:
//   - Block constructs (code blocks, divs, tables, collapsibles, notes)
//   - Paragraph wrapping (handled by Phase 11 on the main buffer)
//   - Images (rare in headings and would require block-context)
//   - Footnote refs (would produce dangling back-links in TOC)
//   - Comment blocks [!-- --] (stripped)
//   - Smart punctuation (already applied on main buffer; headings
//     captured before Phase 8.5, but re-running here is harmless
//     and keeps TOC typography consistent)
//
// The function runs inlineOnly first, then applies size/span passes
// with a fixed-point loop so nested constructs resolve correctly.
func renderWikidotHeadingInline(text string) string {
	// Strip wikidot comments first so their content doesn't leak
	// into the TOC.
	text = reWDComment.ReplaceAllString(text, "")

	// Run the core inline formatting + links pass.
	text = inlineOnly(text)

	// Apply [[size …]]…[[/size]] and balanced [[span …]]…[[/span]]
	// using a fixed-point loop (same strategy as the main body's
	// Phase 1k), since spans can nest and contain size, and size
	// can contain spans.
	prev := ""
	for prev != text {
		prev = text
		// Size: [[size=120%]]…[[/size]] and [[size 120%]]…[[/size]]
		// (single regex handles both = and space forms; value
		// validated through resolveSizeCss).
		text = reWDSize.ReplaceAllStringFunc(text, func(s string) string {
			m := reWDSize.FindStringSubmatch(s)
			css, ok := resolveSizeCss(m[1])
			if !ok {
				return m[2]
			}
			return fmt.Sprintf(`<span style="font-size:%s">%s</span>`, css, m[2])
		})
		// Balanced spans (class / style / multi-attr, including ruby/rt/rb/keycap)
		text = renderBalancedSpans(text)
	}

	return text
}

// renderWikidotCellInline is the cell-scope sibling of inlineOnly.
//
// The `|| … ||` table row renderer (renderWikidotTableRowLine) used to
// run every cell through inlineOnly, which intentionally skips block
// constructs and ALL `[[…]]` multi-token forms (size, span, anchor
// def, comments). That's wrong for tables: rule-wiki's [[size X]]
// examples, [[span style=…]] samples, etc. all live inside a `||`
// cell, and Wikidot re-runs the inline stack there. The narrower
// inlineOnly missed every one of those. Without this pass a reader
// sees:
//
//	|| {{@@//斜体//@@}} || //斜体// ||
//
// as raw text on the second column.
//
// What we add on top of inlineOnly:
//
//   - [!-- … --] (Wikidot strips comments in TD the same as body).
//   - `[[size X]]Y[[/size]]` → <span style="font-size:…">Y</span>.
//   - `[[span style=…]]Y[[/span]]` → <span style="…">Y</span>.
//   - `[[span class=…]]Y[[/span]]` → handled by the balanced matcher
//     (renderBalancedSpanClass), which supports Devanos's ruby/rt
//     nesting (one [[span class="ruby"]] …
//     [[span class="rt"]]Y[[/span]][[/span]]).
//
// We deliberately do NOT run the full convert() pipeline here:
//   - [[code]] / [[html]] / [[collapsible]] / [[table]] block-stashes
//     inside cells shouldn't apply (a [[code]] in a TD has no place;
//     it would just be literal text).
//   - Heading detection would turn anything starting with `+ ` into a
//     `<hN>` which is also invalid inside a TD.
//   - [[note]] likewise — Wikidot's [[note]] is a block construct and
//     would violate HTML semantics inside <td>.
//
// Therefore this function only runs the cell-safe inline passes.
// Multi-pass invariant: anything that mutates text references itself
// to keep the inner content of generated spans consistent (e.g. a
// span-class containing size, etc.).
func slugifyUsername(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '_' || r == '-':
			b.WriteRune('-')
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		// Edge case: name is all non-alphanumeric
		// (e.g. `[[user +]]`). Fall back to a
		// numeric stub so the link doesn't 404
		// silently — the slug is non-empty and
		// obvious as a placeholder.
		return "user"
	}
	return out
}

// slugifyWikidotPageName converts a Wikidot page title to its URL slug,
// following Wikidot conventions: lowercase, spaces and punctuation
// collapse to hyphens, leading/trailing hyphens stripped. Category
// prefix ("category:page") is preserved.
func slugifyWikidotPageName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Handle category prefix (e.g., "category:page name")
	catPrefix := ""
	if idx := strings.Index(name, ":"); idx >= 0 {
		// Check if this looks like a category prefix (no / before colon,
		// and the part before colon looks like a category name)
		before := name[:idx]
		if !strings.Contains(before, "/") && !strings.Contains(before, " ") {
			catPrefix = strings.ToLower(before) + ":"
			name = name[idx+1:]
		}
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32) // to lowercase
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '\t':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		case r == '"' || r == '\'' || r == ';' || r == ',' || r == '.' || r == '!' || r == '?' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}':
			// Punctuation → hyphen
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			// Other unicode letters/digits are preserved
			// (matches our backend slug policy allowing Unicode)
			b.WriteRune(r)
			lastDash = false
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return catPrefix + "page"
	}
	return catPrefix + out
}

// processWikidotLink handles `[[[target|alias]]]` triple-bracket links.
// Supports:
//   - External URLs: [[[http(s)://...|text]]]
//   - Relative paths: [[[/path|text]]]
//   - Internal pages: [[[page-name|text]]] (auto-slugified)
//   - Anchors: [[[page#anchor|text]]] or [[[#anchor|text]]]
//   - New-tab links: [[[*http://...|text]]]
//   - Empty alias: [[[page|]]] uses the page name as text
func processWikidotLink(target, alias string) string {
	// Check for new-tab marker * on external URLs
	newTab := false
	if strings.HasPrefix(target, "*http://") || strings.HasPrefix(target, "*https://") {
		newTab = true
		target = target[1:]
	}
	// Split off anchor fragment
	anchor := ""
	if idx := strings.Index(target, "#"); idx >= 0 {
		anchor = target[idx:] // includes the #
		target = target[:idx]
	}
	// Determine display text
	text := alias
	if text == "" {
		text = target
		// If no alias and no anchor, use the original target as text.
		// But if target is empty (anchor-only like [[[#toc1|]]]), use the anchor.
		if text == "" && anchor != "" {
			text = anchor
		}
	}
	// Build the href
	var href string
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		href = target
	} else if strings.HasPrefix(target, "/") {
		href = target
	} else if target == "" && anchor != "" {
		// Anchor-only link (same page)
		href = anchor
	} else if target != "" {
		// Internal wiki page — auto-slugify
		slug := slugifyWikidotPageName(target)
		href = "/wikidot/" + slug
	} else {
		// Empty target with no anchor — treat as text only
		return html.EscapeString(text)
	}
	href = href + anchor
	safe := sanitizeURLForAttr(href)
	if safe == "" {
		return html.EscapeString(text)
	}
	attrs := ""
	if newTab || strings.HasPrefix(safe, "http://") || strings.HasPrefix(safe, "https://") {
		if strings.HasPrefix(safe, "/wikidot/") || strings.HasPrefix(safe, "#") {
			// Internal links don't need new tab
		} else {
			attrs = ` rel="nofollow noopener" target="_blank"`
		}
	}
	if newTab {
		attrs = ` rel="nofollow noopener" target="_blank"`
	}
	return fmt.Sprintf(`<a href="%s"%s>%s</a>`, safe, attrs, html.EscapeString(text))
}

// renderWikidotDefList converts a run of `: term : definition`
// lines (and their `:` continuations) into ONE
// `<dl>...</dl>` HTML block. Consecutive `: term : def`
// lines (possibly interleaved with continuation lines)
// share a single `<dl>`. A blank line or any other
// non-def line ends the current `<dl>`.
//
// IMPORTANT: the rendered block is emitted as a SINGLE
// LINE (no internal `\n`) so the wrap phase treats it as
// one block-level element and never wraps the inner
// `<dt>` / `<dd>` in `<p>` tags. We use `\n` only as the
// boundary between the def-list run and the surrounding
// text. The pre-pass at the end of the function
// collapses any `\n\n+` around the `<dl>` back to a
// single newline so the surrounding paragraph
// structure isn't disturbed.
var reWDInnermostSpanClass = regexp.MustCompile(`(?is)\[\[span class="([^"]*)"\]\]([^[]*?)\[\[/span\]\]`)

// reWDInnermostSpanStyle captures the innermost `[[span style="X"]]INNER[[/span]]`.
var reWDInnermostSpanStyle = regexp.MustCompile(`(?is)\[\[span style="([^"]*)"\]\]([^[]*?)\[\[/span\]\]`)

// reWDInnermostSpanGeneric captures the innermost multi-attribute span
// `[[span key="value" key="value" ...]]INNER[[/span]]`.
var reWDInnermostSpanGeneric = regexp.MustCompile(`(?is)\[\[span((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")+)\s*\]\]([^[]*?)\[\[/span\]\]`)

// renderBalancedSpanClass replaces balanced
// `[[span class="X"]]...[[/span]]`, `[[span style="X"]]...[[/span]]`,
// and multi-attribute `[[span key="v" ...]]...[[/span]]` constructs
// in `s` with their HTML5 equivalents.
//
// Mapping for class-only spans:
//   - `class="keycap"`         → `<kbd>…</kbd>`
//   - `class="ruby"`           → `<ruby>…</ruby>`
//   - `class="rt"`             → `<rt>…</rt>`     (used nested inside ruby)
//   - `class="rb"`             → `<rb>…</rb>`     (used nested inside ruby)
//   - `class="foo bar"`        → `<span class="foo bar">…</span>`
//   - any class with an invalid token → wrapper dropped, inner kept verbatim
//   - empty class                       → wrapper dropped, inner kept verbatim
//
// Style-only spans become `<span style="…">…</span>` (CSS sanitized).
// Multi-attribute spans combine class and style into one `<span>` tag.
//
// This is the substitution function used by Phase 1k. It replaces the
// previous single-pass regex (and the separate reWDSpanStyle pass)
// with a fixed-point loop that handles all three forms, including
// nested ruby/rt/rb cases.
func renderBalancedSpans(s string) string {
	if !strings.Contains(s, "[[span ") {
		return s
	}
	prev := ""
	for prev != s {
		prev = s
		// Generic multi-attribute first (longest match)
		s = reWDInnermostSpanGeneric.ReplaceAllStringFunc(s, func(full string) string {
			m := reWDInnermostSpanGeneric.FindStringSubmatch(full)
			return mapSpanAttrsToElement(m[1], m[2])
		})
		// Then class-only
		s = reWDInnermostSpanClass.ReplaceAllStringFunc(s, func(full string) string {
			m := reWDInnermostSpanClass.FindStringSubmatch(full)
			return mapSpanClassToElement(m[1], m[2])
		})
		// Then style-only
		s = reWDInnermostSpanStyle.ReplaceAllStringFunc(s, func(full string) string {
			m := reWDInnermostSpanStyle.FindStringSubmatch(full)
			if css := sanitizeCSSValue(m[1]); css != "" {
				return fmt.Sprintf(`<span style="%s">%s</span>`, css, m[2])
			}
			return m[2]
		})
	}
	return s
}

// mapSpanAttrsToElement handles the multi-attribute span form.
func mapSpanAttrsToElement(attrStr, inner string) string {
	attrs := reWDGenericAttr.FindAllStringSubmatch(attrStr, -1)
	if len(attrs) == 0 {
		return inner
	}
	var classVal, styleVal string
	var dataAttrs []string
	for _, a := range attrs {
		key := strings.ToLower(strings.TrimSpace(a[1]))
		val := a[2]
		switch key {
		case "class":
			classVal = val
		case "style":
			if css := sanitizeCSSValue(val); css != "" {
				styleVal = css
			}
		default:
			if strings.HasPrefix(key, "data-") {
				name := key[5:]
				valid := true
				for _, c := range name {
					if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
						(c >= '0' && c <= '9') || c == '-' || c == '_') {
						valid = false
						break
					}
				}
				if valid {
					dataAttrs = append(dataAttrs, fmt.Sprintf(`%s="%s"`, key, html.EscapeString(val)))
				}
			}
		}
	}
	// Collect non-class attributes that should be preserved even on semantic elements
	var extraAttrs []string
	if styleVal != "" {
		extraAttrs = append(extraAttrs, fmt.Sprintf(`style="%s"`, styleVal))
	}
	extraAttrs = append(extraAttrs, dataAttrs...)
	extraAttrStr := ""
	if len(extraAttrs) > 0 {
		extraAttrStr = " " + strings.Join(extraAttrs, " ")
	}
	// Handle special semantic classes (ruby/rt/rb/keycap)
	if classVal != "" {
		switch strings.ToLower(strings.TrimSpace(classVal)) {
		case "keycap":
			return fmt.Sprintf(`<kbd%s>%s</kbd>`, extraAttrStr, inner)
		case "ruby":
			return fmt.Sprintf(`<ruby%s>%s</ruby>`, extraAttrStr, inner)
		case "rt":
			return fmt.Sprintf(`<rt%s>%s</rt>`, extraAttrStr, inner)
		case "rb":
			return fmt.Sprintf(`<rb%s>%s</rb>`, extraAttrStr, inner)
		}
	}
	// Build the <span> tag
	var attrParts []string
	if classVal != "" {
		// Validate class tokens
		parts := strings.Fields(classVal)
		cleaned := make([]string, 0, len(parts))
		valid := true
		for _, p := range parts {
			if p == "" || !isValidClassName(p) {
				valid = false
				break
			}
			cleaned = append(cleaned, p)
		}
		if valid && len(cleaned) > 0 {
			attrParts = append(attrParts, fmt.Sprintf(`class="%s"`, strings.Join(cleaned, " ")))
		}
	}
	if styleVal != "" {
		attrParts = append(attrParts, fmt.Sprintf(`style="%s"`, styleVal))
	}
	attrParts = append(attrParts, dataAttrs...)
	if len(attrParts) == 0 {
		return inner
	}
	return fmt.Sprintf(`<span %s>%s</span>`, strings.Join(attrParts, " "), inner)
}

// mapSpanClassToElement translates one balanced [[span class="X"]]pair[[/span]]
// to its HTML5 equivalent (or a generic `<span class>` for non-special
// classes). The HTML-escape for the inner content is the caller's job
// when the inner has user-controlled text; this helper preserves text
// verbatim so any pre-existing HTML (from a previous pass of the loop)
// survives the wrapping.
func mapSpanClassToElement(cls, inner string) string {
	switch strings.ToLower(strings.TrimSpace(cls)) {
	case "keycap":
		return `<kbd>` + inner + `</kbd>`
	case "ruby":
		return `<ruby>` + inner + `</ruby>`
	case "rt":
		return `<rt>` + inner + `</rt>`
	case "rb":
		return `<rb>` + inner + `</rb>`
	case "":
		return inner
	}
	// Generic class — sanitise each whitespace-separated token.
	// Any invalid token drops the wrapper entirely so a hostile
	// `class="x onclick=…"` can't inject attributes: a partially-
	// accepted multi-class would still be a footgun if the bad
	// token happened to be a valid HTML attribute name.
	parts := strings.Fields(cls)
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || !isValidClassName(p) {
			return inner
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		return inner
	}
	return `<span class="` + strings.Join(cleaned, " ") + `">` + inner + `</span>`
}

// isValidClassName returns true iff s consists only of
// [A-Za-z0-9_-]+ (i.e. a single CSS class identifier, no spaces,
// no escapes). The intent matches the previous
// `sanitizeAnchorID` rule but applied per-token so multi-class
// `class="foo bar"` strings survive the round-trip.
func isValidClassName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// applyWikidotInlineColor is the substitution function for
// reWDInlineColor. It's shared between the main Phase 2 inline
// pass (line ~1605) and the recursive inlineOnly() helper that
// other phases call back into for list items / table cells /
// span-class inner content — the two sites need to make the
// same decision about which colour form the user wrote.
//
// Recognised forms (matches `##<color>|<text>##`):
//   - `##<name>|text##`          — `name` is looked up in
//     the `colorNames` table; unknown names drop the wrapper.
//   - `##<#rgb>|text##`          — explicit hex with `#`.
//   - `##<rgb>|text##`           — bare hex without `#`, the form
//     the rule-wiki wikidot-syntax spec uses (e.g. `##44FF88|`).
//     We prepend `#` so CSS parses it correctly. Length must be
//     3/4/6/8 hex digits (CSS hex shorthand + rgba).
//
// Any non-conforming input drops the wrapper and emits the inner
// text verbatim — same fallback as a malformed class.
func applyWikidotInlineColor(s string) string {
	m := reWDInlineColor.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	name := strings.TrimSpace(m[1])
	text := m[2]

	// Hex (with or without `#`).
	hex := name
	if !strings.HasPrefix(hex, "#") && isPureHex(hex) {
		hex = "#" + hex
	}
	if strings.HasPrefix(hex, "#") {
		if css := sanitizeCSSValue(hex); css != "" {
			// CSS hex is 3, 4, 6, or 8 digits after the `#`.
			n := len(css) - 1
			if n == 3 || n == 4 || n == 6 || n == 8 {
				return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, html.EscapeString(text))
			}
		}
		return text
	}

	// Named colour.
	if css, ok := colorNames[strings.ToLower(name)]; ok {
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, html.EscapeString(text))
	}
	return text
}

// isPureHex reports true iff s is non-empty and contains only
// `[0-9A-Fa-f]`. Used by the inline-color handler to decide
// whether a colour value is a bare hex literal (the spec form)
// vs a CSS-named token.
func isPureHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
