package parsers

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

func renderCodeBlock(code, lang string) string {
	c := html.EscapeString(code)
	preClass := "wikidot-code"
	codeClass := ""
	if lang != "" {
		codeClass = fmt.Sprintf(` class="language-%s"`, lang)
	}
	return fmt.Sprintf(`<pre class="%s"><code%s>%s</code></pre>`, preClass, codeClass, c)
}

// renderEmailLink turns a single address (no brackets, no
// surrounding markup) into the obfuscated `<a class="wikidot-email">`
// markup. The address is split at the LAST `@` into `user` /
// `domain` halves and stored in `data-` attributes; the visible
// link text is the assembled address so the user sees a normal
// mailto link, but a naive HTML scraper looking for the `@` will
// only find it in the rendered text (and modern scrapers already
// extract from there — this is a low-grade obfuscation, not a
// serious anti-harvest measure). When the address is malformed
// (no `@`) we fall back to plain text.
func headingRegexFor(level int) *regexp.Regexp {
	switch level {
	case 1:
		return reWDH1_
	case 2:
		return reWDH2_
	case 3:
		return reWDH3_
	case 4:
		return reWDH4_
	case 5:
		return reWDH5_
	case 6:
		return reWDH6_
	}
	return nil
}

func headingStarRegexFor(level int) *regexp.Regexp {
	switch level {
	case 1:
		return reWDH1Star
	case 2:
		return reWDH2Star
	case 3:
		return reWDH3Star
	case 4:
		return reWDH4Star
	case 5:
		return reWDH5Star
	case 6:
		return reWDH6Star
	}
	return nil
}

// collectFootnoteDefs pre-scans the source for `^[ \t]*[N] text` lines
// (footnote DEFINITIONS, not body references) and stashes them so the
// inline pass doesn't re-process them as body references. The original
// line is replaced with a placeholder, which the paragraph-wrapper
// recognises as a block boundary and emits as an empty <p>. After
// Phase 13's footnote-list append, the visible effect is: definitions
// disappear from the body and re-appear as a numbered <ol> at the
// bottom.
func renderWikidotFormBlocks(source string) string {
	const openTok = "[[form"
	const closeTok = "[[/form]]"
	var sb strings.Builder
	i := 0
	for i < len(source) {
		// Find next opener. We require `[[form` followed by either
		// whitespace + attrs, OR `]]` directly (the bare-form
		// form), OR by the closing `]]`. The substring match is
		// restricted to the literal `[[form` so `[[formula]]`
		// (false-positive) doesn't trip us.
		openerStart := indexOfFormOpener(source, i)
		if openerStart < 0 {
			sb.WriteString(source[i:])
			return sb.String()
		}
		// Emit everything before the opener verbatim.
		sb.WriteString(source[i:openerStart])
		// Find the close `]]` of the opener tag itself.
		closeIdx := strings.Index(source[openerStart:], "]]")
		if closeIdx < 0 {
			// Unbalanced opener with no `]]` — leave verbatim.
			sb.WriteString(source[openerStart:])
			return sb.String()
		}
		openerEnd := openerStart + closeIdx + 2
		openerTag := source[openerStart:openerEnd]
		// Byte-level depth-counted walk for the matching close.
		// Walk from `openerStart` (NOT `openerEnd`) so that the
		// walk encounters the opener we just found. Start
		// `depth` at 0; the opener we re-encounter sets depth=1.
		// A close that brings depth back to 0 is the matching
		// close for the outer opener — anything between the
		// openerEnd and that close is the form body.
		j := openerStart
		depth := 0
		for j < len(source) {
			// Check for next opener at the current position:
			// require `[[form` followed by a non-name char.
			if isAtFormOpener(source, j) {
				// Find this opener's own `]]` to know the full token
				// length.
				cl := strings.Index(source[j:], "]]")
				if cl < 0 {
					// Defensive — opener found but no close.
					sb.WriteString(source[openerStart:])
					return sb.String()
				}
				depth++
				j = j + cl + 2
				continue
			}
			// Check for next close `[[/form]]`.
			if strings.HasPrefix(source[j:], closeTok) {
				depth--
				closeEnd := j + len(closeTok)
				if depth == 0 {
					body := source[openerEnd:j]
					inner := substituteFormWidgets(body)
					sb.WriteString(`<form`)
					// Pull the attribute tail out of the opener tag.
					sb.WriteString(extractFormAttrs(openerTag))
					sb.WriteString(`>`)
					sb.WriteString(inner)
					sb.WriteString(`</form>`)
					i = closeEnd
					goto nextBlock
				}
				j = closeEnd
				continue
			}
			j++
		}
		// EOF without a matching close — leave the opener raw.
		sb.WriteString(source[openerStart:])
		return sb.String()
	nextBlock:
	}
	return sb.String()
}

// isAtFormOpener reports whether `source` at-or-after `i`
// starts with a valid `[[form` opener token (i.e. `[[form`
// followed by whitespace, which means at least one
// attribute is being defined). Bare `[[form]]` (no
// attributes, just the bare form) is intentionally NOT
// recognised as a form opener — authors who write a bare
// `[[form]]` token mid-text (in a heading, a sentence,
// etc.) get the literal text preserved, not a phantom
// form block. The form block construct always carries at
// least one attribute pair in wikidot's spec, so
// requiring whitespace after `[[form` is consistent
// with the documented convention.
func isAtFormOpener(source string, i int) bool {
	// `[[form` is exactly 6 chars; we need at least one
	// more char after to discriminate `[[form` from
	// `[[formula]]`. The byte right after the opener is
	// at offset `i + 6`.
	if i+6 >= len(source) {
		return false
	}
	if !strings.HasPrefix(source[i:], "[[form") {
		return false
	}
	after := i + 6
	// Require a whitespace char (space, tab, newline,
	// CR). Other chars (`]`, `=`, `>`, `/`) would be
	// ambiguous with a wiki-link opener (`[[form|alias]]`,
	// for example — though we don't currently support
	// the pipe alias form on form either, but rejecting
	// these keeps the token boundary strict).
	switch source[after] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// indexOfFormOpener returns the byte position of the next
// `[[form` literal that is followed by either whitespace,
// `=`, `>`, or `]]` (so `[[formula` or `[[formation]]` are
// not mistaken for a form opener). We check the byte right
// after `[[form` to disambiguate.
func indexOfFormOpener(source string, from int) int {
	const tok = "[[form"
	idx := from
	for idx < len(source) {
		j := strings.Index(source[idx:], tok)
		if j < 0 {
			return -1
		}
		at := idx + j
		after := at + len(tok)
		if after >= len(source) {
			return -1
		}
		// The next char must be whitespace — bare `[[form]]`
		// (no attrs) is intentionally NOT a form opener; an
		// author who writes a literal `[[form]]` token in
		// a heading or sentence keeps the literal text
		// visible. This matches `isAtFormOpener`.
		switch source[after] {
		case ' ', '\t', '\n', '\r':
			return at
		}
		idx = at + 1
	}
	return -1
}

// extractFormAttrs pulls the attribute tail out of `[[form …]]`
// and returns it as a string ready to splice into the `<form>`
// tag (e.g. ` method="post" action="/api/submit"`). The
// opening `[[form` and trailing `]]` are dropped. Empty string
// when the author wrote a bare `[[form]]` with no attributes.
//
// We sanity-check each `key="value"` pair through
// `sanitizeAnchorID` (key) and HTML escape (value) so the
// resulting `<form>` tag is safe to drop in. Unknown keys are
// passed through (CSS-style attributes with custom names are
// the author's choice; the CSP front-end enforces policy on
// the runtime side).
func extractFormAttrs(opener string) string {
	// opener is `[form …]`. Strip the opener/closer.
	const openTok = "[[form"
	const closeTok = "]]"
	if !strings.HasPrefix(opener, openTok) {
		return ""
	}
	rest := opener[len(openTok):]
	if !strings.HasSuffix(rest, closeTok) {
		return ""
	}
	rest = rest[:len(rest)-len(closeTok)]
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	// Validate each `key="value"`. We re-use reWDImageAttr
	// (it's the standard `key="value"` regex the wikidot
	// parser uses everywhere). Anything not matching the
	// regex pair is dropped (the author sees only what they
	// wrote, minus the bad pair, since we replace-on-match).
	var out []string
	for _, m := range reWDImageAttr.FindAllStringSubmatch(rest, -1) {
		key := sanitizeAnchorID(m[1])
		val := html.EscapeString(m[2])
		if key == "" || val == "" {
			continue
		}
		out = append(out, fmt.Sprintf(` %s="%s"`, key, val))
	}
	return strings.Join(out, "")
}

// substituteFormWidgets walks a `[[form …]]…[[/form]]` body
// and replaces each form-widget construct (input / textarea /
// button / checkbox / radio / select / option) with the
// matching HTML form element. Prose between widgets is run
// through `inlineOnly` so wikidot inline formatting survives.
//
// Nested `[[form …]]…[[/form]]` blocks (an inner form inside
// the outer form body) are processed by RE-INVOKING
// renderWikidotFormBlocks on the body. The outer render pass
// matched only one `[`[`form…`]`]`-`[`/`form`]` pair, but the
// BODY may contain a nested form which needs its own
// processing; running renderWikidotFormBlocks recursively
// handles that.
//
// Strategy: substitute each PAIR (opener + close) in TWO
// passes — first the openers, then the closes. This avoids
// the closure-captured-body pitfall (ReplaceAllStringFunc
// only replaces the matched span, leaving text AFTER the
// match untouched — so capturing the body and slicing beyond
// the match double-emits the inner content). After both
// passes the body has plain HTML form elements with the
// original prose between them, and inlineOnlyProse only has
// to walk plain HTML+text.
//
// Order of substitution:
//  1. Nested form blocks first (recursive call) — otherwise
//     a `[[form …]]` inside the body would survive to the
//     widget passes and confuse them.
//  2. Single-tag widgets (input / checkbox / radio /
//     form-button) — their `[[input …]]` opener also matches
//     when followed by another `]]` in the source, so
//     substituting them first removes that ambiguity.
//  3. `<textarea>` opener → `<textarea …>` and
//     `[[/textarea]]` → `</textarea>` as separate passes.
//  4. `<select>` and `<option>` follow the same pattern.
//  5. inlineOnlyProse on the resulting body.
func substituteFormWidgets(body string) string {
	// Recurse into any nested form blocks first. We
	// re-invoke renderWikidotFormBlocks (the depth-
	// counter walker) on the body — it can find and
	// process a nested `[[form …]]…[[/form]]` while
	// leaving non-form prose intact.
	body = renderWikidotFormBlocks(body)
	body = reWDInput.ReplaceAllStringFunc(body, func(s string) string { return renderInputTag(s) })
	body = reWDCheckbox.ReplaceAllStringFunc(body, func(s string) string { return renderCheckboxTag(s) })
	body = reWDRadio.ReplaceAllStringFunc(body, func(s string) string { return renderRadioTag(s) })
	body = reWDFormButton.ReplaceAllStringFunc(body, func(s string) string { return renderButtonTag(s) })
	body = reWDTextareaOpen.ReplaceAllStringFunc(body, func(s string) string {
		m := reWDTextareaOpen.FindStringSubmatch(s)
		return "<textarea" + m[1] + ">"
	})
	body = reWDTextareaClose.ReplaceAllString(body, "</textarea>")
	body = reWDSelectOpen.ReplaceAllStringFunc(body, func(s string) string {
		m := reWDSelectOpen.FindStringSubmatch(s)
		return "<select" + m[1] + ">"
	})
	body = reWDSelectClose.ReplaceAllString(body, "</select>")
	body = reWDOptionOpen.ReplaceAllStringFunc(body, func(s string) string {
		m := reWDOptionOpen.FindStringSubmatch(s)
		return "<option" + m[1] + ">"
	})
	body = reWDOptionClose.ReplaceAllString(body, "</option>")
	body = inlineOnlyProse(body)
	return body
}

// inlineOnlyProse is a pass-through to inlineOnly that's used
// as the form-body default — split into its own helper so
// future tweaks (e.g. escape sensitive characters differently
// inside forms) have a clean insertion point.
func renderInputTag(s string) string {
	m := reWDInput.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	if attrs["type"] == "" {
		attrs["type"] = "text"
	}
	return buildSelfClosingWidget("input", attrs)
}

// renderCheckboxTag turns `[[checkbox …]]` into
// `<input type="checkbox" … />`. A bare `checked` attribute
// (no value) is preserved on the element so the box renders
// pre-checked. `checked="false"` is dropped (the absence of
// the attribute is the "unchecked" state).
func renderCheckboxTag(s string) string {
	m := reWDCheckbox.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	attrs["type"] = "checkbox"
	return buildSelfClosingWidget("input", attrs)
}

// renderRadioTag turns `[[radio …]]` into `<input type="radio" … />`.
// Same posture as checkbox — `checked` is preserved when present.
func renderRadioTag(s string) string {
	m := reWDRadio.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	attrs["type"] = "radio"
	return buildSelfClosingWidget("input", attrs)
}

// renderButtonTag turns `[[button …]]` into `<button …>label</button>`
// where `label` is taken from the `label="…"` attribute (the
// wikidot convention — HTML <button> uses inner text, not
// a label attribute). When `label` is omitted we render the
// button with the literal text "Submit" as a sensible default.
func renderButtonTag(s string) string {
	m := reWDFormButton.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	label := attrs["label"]
	if label == "" {
		label = "Submit"
	}
	delete(attrs, "label")
	if attrs["type"] == "" {
		attrs["type"] = "submit"
	}
	return buildPairedWidget("button", attrs, label)
}

// renderTextareaTag turns `[[textarea attrs]]content[[/textarea]]`
// into `<textarea attrs>content</textarea>`. The body is
// HTML-escaped at substitution time but already went through
// inlineOnly first so links / bold become real HTML before
// being inserted (no double-escape).
func renderTextareaTag(attrs string, inner string) string {
	a := parseWidgetAttrs(attrs)
	return buildPairedWidget("textarea", a, inner)
}

// renderSelectTag turns `[[select attrs]]…options…[[/select]]`
// into `<select attrs>…options…</select>`. The inner prose
// has already been substituted by the parent loop, so we
// just wrap it.
func renderSelectTag(attrs string, inner string) string {
	a := parseWidgetAttrs(attrs)
	return buildPairedWidget("select", a, inner)
}

// renderOptionTag turns `[[option attrs]]label[[/option]]`
// into `<option attrs>label</option>`. The label is run
// through inlineOnly by the outer loop so wikidot inline
// formatting (e.g. `//italic//` for an option label)
// survives. We do NOT entity-escape here — the upstream
// inlineOnly pass is already safe.
func renderOptionTag(attrs string, label string) string {
	a := parseWidgetAttrs(attrs)
	return buildPairedWidget("option", a, label)
}

// parseWidgetAttrs parses the attribute tail shared by every
// form widget (`[[TAG attrs]]`). Keys are lowercased; values
// are HTML-escaped; bare keys (HTML5 boolean attributes like
// `checked`, `selected`, `required`, `disabled`, `autofocus`)
// are recorded with an empty value so the renderer knows to
// emit them as bare attributes.
//
// Unknown keys (`placeholder`, `pattern`, `min`, `max`,
// `step`, `data-*`, etc.) pass through sanitisation
// unchanged — that's by design, since wikidot doesn't want
// to maintain an allow-list of every HTML5 attribute.
func renderWikidotIndentBlocks(source string, p *wikidotParser) string {
	const open = "[[indent]]"
	const close = "[[/indent]]"
	var sb strings.Builder
	i := 0
	for i < len(source) {
		oi := strings.Index(source[i:], open)
		if oi < 0 {
			sb.WriteString(source[i:])
			return sb.String()
		}
		// Emit the prefix up to the open tag.
		sb.WriteString(source[i : i+oi])
		blockStart := i + oi + len(open)
		// Walk from blockStart, counting nested
		// opens until we find the matching close.
		depth := 1
		j := blockStart
		for j < len(source) {
			nextOpen := strings.Index(source[j:], open)
			nextClose := strings.Index(source[j:], close)
			if nextClose < 0 {
				// Unmatched — leave the
				// open tag raw, plus
				// everything after it (the
				// author can see what they
				// wrote).
				sb.WriteString(source[i+oi:])
				return sb.String()
			}
			if nextOpen >= 0 && nextOpen < nextClose {
				depth++
				j += nextOpen + len(open)
				continue
			}
			depth--
			closeEnd := j + nextClose + len(close)
			if depth == 0 {
				body := source[blockStart : j+nextClose]
				// `inlineOnly` keeps inline
				// formatting (bold / italic /
				// links) but doesn't emit
				// block-level wrappers. The
				// newline-to-`<br />` rewrite
				// lets multi-line bodies
				// render without dragging a
				// `<p>` into the indent div.
				// First re-process the body
				// so nested `[[indent]]`
				// blocks become their own
				// `<div>`s — otherwise a
				// nested indent would stay
				// as raw text inside the
				// outer body. We recurse via
				// `renderWikidotIndentBlocks`
				// first; if the body has
				// nested indents they'll
				// come out as `<div>`s, and
				// the surrounding inline
				// text gets the newline
				// rewrite.
				preNested := renderWikidotIndentBlocks(body, p)
				inner := strings.TrimSpace(inlineOnly(preNested))
				inner = strings.ReplaceAll(inner, "\n", "<br />")
				sb.WriteString(`<div class="wikidot-indent">`)
				sb.WriteString(inner)
				sb.WriteString(`</div>`)
				i = closeEnd
				goto nextBlock
			}
			j = closeEnd
		}
		// Reached EOF without finding a matching
		// close at depth 1 — leave the open raw.
		sb.WriteString(source[i+oi:])
		return sb.String()
	nextBlock:
	}
	return sb.String()
}

// renderWikidotTabviews walks `source` left-to-right,
// matching `[[tabview]]…[[/tabview]]` blocks via a
// depth-counter (so a nested `[[tabview]]` inside a
// tab body doesn't confuse the outer match), then
// splits each matched body by `[[tab Title]]…[[/tab]]`
// entries. Output is a `.wikidot-tabview` container
// with:
//   - `<ul class="wikidot-tab-nav">` listing each
//     tab as a `<li class="wikidot-tab-tab">` (the
//     first tab gets `.active`)
//   - `<div class="wikidot-tab-panels">` listing
//     each panel as `<div class="wikidot-tab-panel">`
//     (the first panel gets `.active`)
//
// Each tab and panel share a `data-tab-id="N"`
// attribute (N is the 0-based index) so the
// client-side script (ArticleView.tsx) can match
// nav clicks to panel visibility without re-parsing
// the DOM.
//
// Tab titles are HTML-escaped (they're plain text,
// not wikidot source). Tab bodies are routed through
// `convertNoFootnote` so block-level markup inside
// (lists, blockquotes, code blocks, even nested
// tabviews) still renders — but a tab can't spawn
// its own footnote list (Wikidot's behaviour;
// footnote lists are article-scoped).
//
// renderWikidotSizeBlocks walks `source` left-to-right,
// matching `[[size …]]…[[/size]]` blocks via a depth
// counter (so a nested `[[size …]]` inside the inner
// body doesn't trip the outer close). The recursive
// structure mirrors renderWikidotIndentBlocks.
//
// Each block's body is itself run through this same
// function (via `renderWikidotSizeBlocks(inner)`), so
// nested `[[size …]]` blocks become nested `<span
// style="font-size:…">` wrappers.
//
// Two valid value classes:
//
//  1. Whitelisted keyword (`xx-small`, `larger`, etc. —
//     listed in `sizeMap`); the keyword is converted to
//     a rem value for portability.
//  2. Whitelisted numeric form (`N`, `Npx`, `Nem`,
//     `N%`, with optional fractional part). The regex
//     `reWDSizeValue` validates the pattern; values
//     outside the whitelist degrade to plain text (no
//     `<span style="font-size:giant">` typo-leak).
//
// Anything outside both classes (e.g. `[[size giant]]`,
// `[[size huger]]`) keeps the construct verbatim so the
// author sees the typo.
func renderWikidotSizeBlocks(source string) string {
	const close = "[[/size]]"
	// `reWDSizeOpen` matches only the opener portion of a
	// wikidot size construct — the size value but not the
	// body / close. We need this separate regex because
	// `reWDSize` greedily matches the full construct
	// (`opener … body … close`), which would make the
	// depth-counter below think the opener end is also the
	// close end.
	reWDSizeOpen := regexp.MustCompile(`(?is)\[\[size\s+[^\]]+\]\]`)
	var sb strings.Builder
	i := 0
	for i < len(source) {
		// Find next opener at-or-after i.
		loc := reWDSizeOpen.FindStringIndex(source[i:])
		if loc == nil {
			sb.WriteString(source[i:])
			return sb.String()
		}
		openerStart := i + loc[0]
		openerEnd := i + loc[1]
		// Emit everything verbatim up to the opener.
		sb.WriteString(source[i:openerStart])
		// Extract the opener's size value. We re-match
		// just the opener so we capture only the size
		// token (group 1), not the body of a nested
		// construct.
		m := reWDSizeOpen.FindStringSubmatch(source[openerStart:])
		if m == nil {
			// Defensive — shouldn't fail here since
			// FindStringIndex returned a hit. Emit
			// the opener verbatim and advance.
			sb.WriteString(source[openerStart:openerEnd])
			i = openerEnd
			continue
		}
		// m[0] is the entire opener (e.g. `[[size 0]]`).
		// The size value is between `[[size ` and `]]`.
		// Trim both ends rather than using capture group
		// 1 because the opener regex above doesn't expose
		// a capture — only m[0] is the full match.
		css := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(m[0], "[[size "), "]]"))
		cssAttr, isValid := resolveSizeCss(css)
		// Depth-counted walk for the matching `[[/size]]`.
		j := openerEnd
		depth := 1
		for j < len(source) {
			nextOpen := reWDSizeOpen.FindStringIndex(source[j:])
			nextClose := strings.Index(source[j:], close)
			switch {
			case nextClose < 0:
				// No close — leave the opener raw.
				sb.WriteString(source[openerStart:])
				return sb.String()
			case nextOpen != nil && (j+nextOpen[0]) < (j+nextClose):
				depth++
				j = j + nextOpen[1]
			default:
				depth--
				closeAt := j + nextClose
				closeEnd := closeAt + len(close)
				if depth == 0 {
					body := source[openerEnd:closeAt]
					bodyRendered := renderWikidotSizeBlocks(body)
					if isValid {
						// Size across paragraph boundaries
						// would produce a <span> that
						// straddles the boundary — Phase 11
						// (paragraph wrap) then wraps each
						// chunk in its own <p> and the
						// outer </span> ends up in the
						// wrong paragraph. We close the
						// span at every `\n\n` and reopen
						// after, so each paragraph in the
						// rendered HTML is individually
						// wrapped in its own <span
						// style="font-size:X">…</span>
						// (Phase 11 still wraps each
						// chunk in <p>, which then sits
						// cleanly around the per-paragraph
						// span).
						if strings.Contains(bodyRendered, "\n\n") {
							chunks := strings.Split(bodyRendered, "\n\n")
							for k, chunk := range chunks {
								if k > 0 {
									sb.WriteString("\n\n")
								}
								if chunk == "" {
									continue
								}
								sb.WriteString(`<span style="font-size:`)
								sb.WriteString(cssAttr)
								sb.WriteString(`">`)
								sb.WriteString(chunk)
								sb.WriteString(`</span>`)
							}
						} else {
							sb.WriteString(`<span style="font-size:`)
							sb.WriteString(cssAttr)
							sb.WriteString(`">`)
							sb.WriteString(bodyRendered)
							sb.WriteString(`</span>`)
						}
					} else {
						sb.WriteString(bodyRendered)
					}
					i = closeEnd
					goto nextSize
				}
				j = closeEnd
			}
		}
		// EOF without a matching close — leave raw.
		sb.WriteString(source[openerStart:])
		return sb.String()
	nextSize:
	}
	return sb.String()
}

// resolveSizeCss returns the canonical CSS value for a
// wikidot size token. If the token is a known keyword
// (mapped via `sizeMap`), the rem value is returned with
// `isValid=true`. If the token is a numeric form (`Npx` /
// `Nem` / `N%` / plain `N`), the raw token is returned
// with `isValid=true`. Anything else (including bogus
// keywords like `giant`, or CSS values that bypassed
// `reWDSizeValue`'s whitelist) returns `isValid=false`
// so the caller falls back to the body-only path.
func resolveSizeCss(token string) (string, bool) {
	if v, ok := sizeMap[strings.ToLower(token)]; ok {
		return v, true
	}
	if !reWDSizeValue.MatchString(token) {
		return "", false
	}
	return token, true
}

// renderWikidotTabviews walks `source` left-to-right,
// matching `[[tabview]]…[[/tabview]]` blocks via a
// depth-counter (so a nested `[[tabview]]` inside a tab
// (silent) mistake.
func renderWikidotTabviews(source string, p *wikidotParser) string {
	const open = "[[tabview]]"
	const close = "[[/tabview]]"
	var sb strings.Builder
	i := 0
	for i < len(source) {
		oi := strings.Index(source[i:], open)
		if oi < 0 {
			sb.WriteString(source[i:])
			return sb.String()
		}
		// Emit the prefix up to the tabview opener.
		sb.WriteString(source[i : i+oi])
		blockStart := i + oi + len(open)
		// Walk from blockStart, counting nested
		// `[[tabview]]` opens (so an inner opener
		// doesn't trip the depth back to 0).
		depth := 1
		j := blockStart
		for j < len(source) {
			nextOpen := strings.Index(source[j:], open)
			nextClose := strings.Index(source[j:], close)
			if nextClose < 0 {
				// Unmatched — emit the
				// opener raw, plus
				// everything after.
				sb.WriteString(source[i+oi:])
				return sb.String()
			}
			if nextOpen >= 0 && nextOpen < nextClose {
				depth++
				j += nextOpen + len(open)
				continue
			}
			depth--
			closeEnd := j + nextClose + len(close)
			if depth == 0 {
				body := source[blockStart : j+nextClose]
				// Recurse so a nested
				// `[[tabview]]` inside a
				// tab body gets rendered
				// as its own container,
				// not as raw text.
				preNested := renderWikidotTabviews(body, p)
				tabs := reWDTabItem.FindAllStringSubmatch(preNested, -1)
				if len(tabs) == 0 {
					// No well-formed
					// `[[tab …]]…[[/tab]]`
					// children. Two sub-
					// cases:
					//   1. body has a
					//      `[[tab ` opener
					//      with no matching
					//      `[[/tab]]` —
					//      leave the
					//      opener raw so
					//      the author can
					//      see the typo.
					//   2. body is empty
					//      or has no
					//      `[[tab ` at all
					//      — emit an empty
					//      container.
					if strings.Contains(preNested, "[[tab ") {
						sb.WriteString(source[i+oi : closeEnd])
					} else {
						sb.WriteString(`<div class="wikidot-tabview"></div>`)
					}
					i = closeEnd
					goto nextTab
				}
				sb.WriteString(`<div class="wikidot-tabview">`)
				sb.WriteString(`<ul class="wikidot-tab-nav">`)
				for idx, t := range tabs {
					title := strings.TrimSpace(t[1])
					if idx == 0 {
						sb.WriteString(`<li class="wikidot-tab-tab active" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`"><a href="#" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
						sb.WriteString(html.EscapeString(title))
						sb.WriteString(`</a></li>`)
					} else {
						sb.WriteString(`<li class="wikidot-tab-tab" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`"><a href="#" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
						sb.WriteString(html.EscapeString(title))
						sb.WriteString(`</a></li>`)
					}
				}
				sb.WriteString(`</ul>`)
				sb.WriteString(`<div class="wikidot-tab-panels">`)
				for idx, t := range tabs {
					content := strings.TrimSpace(p.convertNoFootnote(t[2]))
					if idx == 0 {
						sb.WriteString(`<div class="wikidot-tab-panel active" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
					} else {
						sb.WriteString(`<div class="wikidot-tab-panel" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
					}
					sb.WriteString(content)
					sb.WriteString(`</div>`)
				}
				sb.WriteString(`</div>`)
				sb.WriteString(`</div>`)
				i = closeEnd
				goto nextTab
			}
			j = closeEnd
		}
		// Reached EOF without a matching close —
		// emit the opener raw.
		sb.WriteString(source[i+oi:])
		return sb.String()
	nextTab:
	}
	return sb.String()
}

func renderWikidotBlockquotes(text string) string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	// Per-depth line buffer so `>` and `>>` produce nested
	// `<blockquote>` (and `>>>` deep-nests further). Wikidot's
	// convention: each leading `>` adds one indent level. The
	// outer buffer is depth-0 prose; each depth-N buffer holds
	// prose at depth N. A contiguous run of `> text` followed by
	// `>> text` produces `<blockquote>...<blockquote>...</blockquote>`.
	var entries []blockEntry
	flush := func() {
		if len(entries) == 0 {
			return
		}
		result = append(result, renderBlockquoteEntries(entries))
		entries = nil
	}
	for _, line := range lines {
		if m := reWDBlockquote.FindStringSubmatch(line); m != nil {
			// m[1] is the leading `>` chain (possibly followed by
			// a single space); m[2] is the remainder of the line.
			depth := strings.Count(m[1], ">")
			if depth < 1 {
				depth = 1
			}
			// Spec: a `\` at the end of a blockquote line joins
			// onto the next line without a `<br />` between them.
			// Detect by looking at the source line for a trailing
			// `\` (the regex stripped m[2] = "trailing-spaces-stripped"
			// text but the raw character is still on the source line
			// only inside m[2] which our regex does not capture
			// verbatim — re-check the original line).
			joined := strings.TrimRight(m[2], " \t\\")
			// Continuation: if the previous entry was at the same
			// depth, append `joined` to its text with a space, no
			// `<br />`. Otherwise push a new entry.
			if len(entries) > 0 && entries[len(entries)-1].depth == depth && !entries[len(entries)-1].hasBackslash {
				entries[len(entries)-1].text += " " + joined
			} else {
				entries = append(entries, blockEntry{
					depth: depth, text: joined,
					hasBackslash: strings.HasSuffix(strings.TrimRight(m[2], " \t"), "\\"),
				})
			}
		} else {
			flush()
			result = append(result, line)
		}
	}
	flush()
	return strings.Join(result, "\n")
}

type blockEntry struct {
	depth        int
	text         string
	hasBackslash bool
}

// renderBlockquoteEntries walks source-ordered entries and emits
// nested `<blockquote>` HTML. We walk the entries with a depth
// counter and split at every "depth decreased" boundary; each
// boundary closes the inner levels in source order so a run like
//
//	> outer
//	>> nested
//	> back-to-outer
//	> another
//
// yields `<blockquote>outer<blockquote>nested</blockquote>back-to-outer<br>another</blockquote>`.
func renderBlockquoteEntries(entries []blockEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	// Stack of open depths.
	stack := []int{entries[0].depth}
	sb.WriteString(`<blockquote>`)
	sb.WriteString(entries[0].text)
	for i := 1; i < len(entries); i++ {
		e := entries[i]
		d := e.depth
		// Pop until top of stack <= d.
		for len(stack) > 0 && stack[len(stack)-1] > d {
			sb.WriteString(`</blockquote>`)
			stack = stack[:len(stack)-1]
		}
		// Opening new depths: NO `<br />` — the inner `<blockquote>`
		// is structurally INSIDE its parent's content, not a new
		// line in it.
		for j := len(stack); j < d; j++ {
			sb.WriteString(`<blockquote>`)
			stack = append(stack, j+1)
		}
		// Same depth: insert `<br />` separator between sibling
		// lines (since each `<blockquote>` body is a flat text node).
		if len(stack) > 0 && stack[len(stack)-1] == d && i > 0 && entries[i-1].depth == d {
			sb.WriteString(`<br />`)
		}
		sb.WriteString(e.text)
	}
	// Close all open blockquotes.
	for range stack {
		sb.WriteString(`</blockquote>`)
	}
	return sb.String()
}

// renderWikidotLists — rewritten to support nested lists. Each line
// starting with `* ` (unordered) or `# ` (ordered) at a given
// indent depth becomes a <li>; indent is computed as
// `len(leadingSpaces) / 2` (Wikidot's convention is 2 spaces per
// level). Adjacent same-type list items form a <ul>/<ol>; depth
// changes open/close nested lists.
//
// Mixed types (a `* ` line followed by `# ` lines at the same
// level) close the current list and start a new one of the other
// type. This matches Wikidot's own rendering: a depth-1 `#`
// under a depth-1 `*` becomes a child <ol> inside the <li> of
// the surrounding <ul>.
func findNextDivOpen(source string, from int) int {
	// Search for `[[div ` (space after div excludes [[divider]]).
	idx := strings.Index(source[from:], "[[div ")
	if idx < 0 {
		return -1
	}
	return idx + from
}

// parseDivOpen inspects a div open tag at the start of `s` and
// returns:
//   - kind: "class" / "style" / "data" / "multi"
//   - attrValue: for class/style/data — single attr value;
//     for "multi" — the full rendered HTML attribute
//     string (e.g. `class="x" style="y" data-foo="z"`)
//   - contentStart: index (relative to s == source[idx:]) of
//     the first content character
//   - ok: true iff a valid open tag was found
func parseDivOpen(s string) (kind, attrValue string, contentStart int, ok bool) {
	// Generic multi-attribute form first: `[[div key="value" key="value" ...]]`
	// Must start with `[[div ` (space after div excludes [[divider]]).
	if strings.HasPrefix(s, "[[div ") {
		// Skip "[[div " (6 chars) and parse attributes until "]]"
		rest := s[6:]
		// Find the closing "]]" — must be a proper close, not part of an attribute value
		// Walk character by character to respect quoted strings
		closeIdx := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == '"' {
				// Skip quoted string
				i++
				for i < len(rest) && rest[i] != '"' {
					if rest[i] == '\\' {
						i++ // skip escaped char
					}
					i++
				}
				continue
			}
			if i+1 < len(rest) && rest[i] == ']' && rest[i+1] == ']' {
				closeIdx = i
				break
			}
		}
		if closeIdx >= 0 {
			attrStr := rest[:closeIdx]
			// Try multi-attribute parsing
			attrs := reWDGenericAttr.FindAllStringSubmatch(attrStr, -1)
			if len(attrs) > 0 {
				var rendered []string
				consumed := 0
				valid := true
				for _, m := range attrs {
					matchPos := strings.Index(attrStr[consumed:], m[0])
					if matchPos < 0 {
						valid = false
						break
					}
					between := attrStr[consumed : consumed+matchPos]
					for _, c := range between {
						if c != ' ' && c != '\t' && c != '\n' {
							valid = false
							break
						}
					}
					if !valid {
						break
					}
					consumed += matchPos + len(m[0])
					key := strings.ToLower(m[1])
					val := m[2]
					switch key {
					case "class":
						cls := sanitizeAnchorID(val)
						if cls != "" {
							rendered = append(rendered, fmt.Sprintf(`class="%s"`, cls))
						}
					case "style":
						css := sanitizeCSSValue(val)
						if css != "" {
							rendered = append(rendered, fmt.Sprintf(`style="%s"`, css))
						}
					default:
						if strings.HasPrefix(key, "data-") {
							name := key[5:]
							validName := true
							for _, c := range name {
								if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
									(c >= '0' && c <= '9') || c == '-' || c == '_') {
									validName = false
									break
								}
							}
							if validName {
								rendered = append(rendered, fmt.Sprintf(`%s="%s"`, key, html.EscapeString(val)))
							}
						}
					}
				}
				tail := attrStr[consumed:]
				allWhitespace := true
				for _, c := range tail {
					if c != ' ' && c != '\t' && c != '\n' {
						allWhitespace = false
						break
					}
				}
				if valid && allWhitespace && len(rendered) > 0 {
					fullAttrs := strings.Join(rendered, " ")
					return "multi", fullAttrs, 6 + closeIdx + 2, true
				}
			}
		}
	}
	// Legacy single-attribute forms (e.g. [[div class="x"]], [[div style="y"]])
	if strings.HasPrefix(s, "[[div class=\"") {
		rest := s[len("[[div class=\""):]
		// Find closing "]] — but make sure " is immediately followed by ]]
		// (not a space before ]], which would indicate multi-attr form that failed above)
		end := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == '"' && i+2 <= len(rest) && rest[i+1] == ']' && rest[i+2] == ']' {
				end = i
				break
			}
		}
		if end >= 0 {
			attr := rest[:end]
			return "class", attr, len("[[div class=\"") + end + 3, true
		}
	}
	if strings.HasPrefix(s, "[[div style=\"") {
		rest := s[len("[[div style=\""):]
		end := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == '"' && i+2 <= len(rest) && rest[i+1] == ']' && rest[i+2] == ']' {
				end = i
				break
			}
		}
		if end >= 0 {
			attr := rest[:end]
			return "style", attr, len("[[div style=\"") + end + 3, true
		}
	}
	return "", "", 0, false
}

// walkDivBody returns (closeEnd, contentEnd) where contentEnd
// is the index of the first character of the matching `[[/div]]`
// and closeEnd is the index just past the matching close tag
// (i.e. after `]]`). closeEnd == -1 indicates the open tag was
// never closed — caller should leave the construct intact.
func walkDivBody(source string, contentStart int) (closeEnd, contentEnd int) {
	depth := 1
	i := contentStart
	for i < len(source) {
		// Find the next interesting token.
		nextOpen := findNextDivOpen(source, i)
		nextClose := strings.Index(source[i:], "[[/div]]")
		var nextTok int
		var isClose bool
		switch {
		case nextOpen < 0 && nextClose < 0:
			return -1, -1
		case nextOpen < 0:
			nextTok = i + nextClose
			isClose = true
		case nextClose < 0:
			nextTok = nextOpen
			isClose = false
		case nextOpen < i+nextClose:
			nextTok = nextOpen
			isClose = false
		default:
			nextTok = i + nextClose
			isClose = true
		}
		if isClose {
			depth--
			if depth == 0 {
				return nextTok + len("[[/div]]"), nextTok
			}
			i = nextTok + len("[[/div]]")
		} else {
			depth++
			// Skip past the open tag's attribute value.
			_, _, after, ok := parseDivOpen(source[nextTok:])
			if !ok {
				// Malformed — give up so we don't
				// loop forever.
				return -1, -1
			}
			i = nextTok + after
		}
	}
	return -1, -1
}

func wrapWikidotParagraphs(text string) string {
	// No local placeholder stash here — block-level HTML
	// (pre/table/div/ul/ol/blockquote/details/summary) is
	// already stashed via `p.storeBlock` in Phase 1 and
	// restored in Phase 10. A previous version of this
	// function ALSO stashed block HTML into a local
	// `%%WRAP_BLOCK_N%%` map, but that was redundant AND
	// actively wrong: the placeholders landed inside `<p>…</p>`
	// wrappers (the wrap pass treated them as inline text),
	// then the local restore expanded them back into the
	// middle of the `<p>`, producing invalid
	// `<p><div>…</div></p>` HTML. Trust the Phase 1 stash —
	// by the time this function runs in Phase 11, the only
	// block HTML still in the source is what came in via
	// the placeholder system, and our block-boundary check
	// below correctly skips those lines.

	// `%%BLOCK_N%%` / `%%WRAP_BLOCK_N%%` markers are placeholders
	// for block-level HTML that Phase 10 will restore. A line
	// that contains one in the middle (e.g. `inline prefix
	// %%BLOCK_5%% inline suffix`) needs to be split so the
	// marker gets its own line; otherwise paragraph-wrap would
	// emit `<p>… %%BLOCK_5%% …</p>` and the restored block
	// ends up inside the <p>, producing invalid HTML. The
	// regex `reBlockMarkerInLine` is declared at package level.

	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	var buf []string
	// blockInBuf detects whether a line in the buffer opens a
	// block-level element. If so, the whole buffer is emitted
	// verbatim (no <p> wrap) so we don't end up with invalid
	// `<p><div>…</div></p>` or `<p><table>…</table></p>` HTML.
	// Wikidot's block syntax ([[div …]], [[table …]], [[code]],
	// etc.) usually goes through a Phase-1 stash and is restored
	// as a placeholder, but a custom block written by the
	// author — or a Phase-2 inline that emits a block — can
	// sneak into the paragraph buffer. We also include `dl`
	// here so the renderWikidotDefList output (a contiguous
	// `<dl>…</dl>` block) doesn't end up with a stray
	// `<p><dl>…</dl></p>` wrap.
	blockInBuf := regexp.MustCompile(`<(?:div|table|pre|ul|ol|dl|h[1-6]|hr|blockquote|details|summary|section|aside)\b`)
	flush := func() {
		if len(buf) > 0 {
			joined := strings.Join(buf, "<br />\n")
			if blockInBuf.MatchString(joined) {
				// Don't wrap — emit the lines joined by
				// a soft break so the resulting DOM
				// matches the source layout.
				result = append(result, joined)
			} else {
				result = append(result, "<p>"+joined+"</p>")
			}
			buf = nil
		}
	}
	// blockTagStart is a "this line opens or closes a block"
	// detector. It's two regexes because the previous single
	// regex `^</?(…)\b` was a category error: `<div` matches
	// `<` then `d` — but the alternation expected `d` to be
	// the literal first character of a tag in the list
	// (`h[1-6]`, `hr`, `li`, …) and `d` isn't any of those, so
	// the whole match silently failed. Splitting the open
	// and close forms fixes the false-negative on `<div …>`
	// and `<pre …>` lines.
	blockOpenStart := regexp.MustCompile(`^<(h[1-6]|hr|li|p|img|blockquote|ul|ol|pre|table|div|details|summary|section|aside)\b`)
	blockCloseStart := regexp.MustCompile(`^</(h[1-6]|hr|li|p|img|blockquote|ul|ol|dl|pre|table|div|details|summary|section|aside)>`)
	// preOpen / preClose pair: when the wrap sees a `<pre>...</pre>`
	// block, it consumes every line from the opener through the
	// closer as a single opaque unit. The lines between are emitted
	// verbatim (no <br /> insertion, no <p> wrapping) so the
	// <pre>'d content keeps its original spacing. Without this
	// special-case, every newline inside `<pre><code>` ends up as
	// `<br />` and the whole body gets wrapped in `<p>`, producing
	// invalid `<p><pre><code>...<br /></code></pre></p>` HTML.
	preOpen := regexp.MustCompile(`(?i)^<pre\b`)
	preClose := regexp.MustCompile(`(?i)^</pre>`)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		// If a `<pre>` line starts a block, accumulate
		// everything up to and including the matching
		// `</pre>` and emit it as a single chunk.
		// Subsequent lines (even ones that match
		// blockOpenStart themselves) get appended to
		// this chunk instead of being processed
		// individually.
		if preOpen.MatchString(trimmed) {
			flush()
			var preBlock []string
			preBlock = append(preBlock, line)
			// Single-line `<pre>…</pre>` case (rare but
			// possible if some upstream pass collapsed the
			// source): emit just that one line.
			if preClose.MatchString(trimmed) {
				result = append(result, preBlock...)
				continue
			}
			i++
			for i < len(lines) {
				preBlock = append(preBlock, lines[i])
				if preClose.MatchString(strings.TrimSpace(lines[i])) {
					break
				}
				i++
			}
			result = append(result, preBlock...)
			continue
		}
		// If a placeholder marker is buried in the middle of a
		// line (e.g. `inline prefix %%BLOCK_5%% inline suffix`),
		// split the line around it: the prefix joins the
		// paragraph buffer, the marker gets its own line (a
		// block boundary), and the suffix starts a fresh
		// paragraph buffer. Without this split the marker
		// would land inside a <p> and the restored block
		// would be wrapped in invalid `<p><div>…</div></p>`.
		if m := reBlockMarkerInLine.FindStringSubmatch(line); m != nil && (m[1] != "" || m[3] != "") {
			prefix := strings.TrimSpace(m[1])
			suffix := strings.TrimSpace(m[3])
			if prefix != "" {
				buf = append(buf, prefix)
			}
			flush()
			result = append(result, m[2])
			if suffix != "" {
				buf = append(buf, suffix)
			}
			continue
		}
		// Treat placeholder markers as block boundaries so a placeholder
		// that resolves to e.g. `<span>...</span>` or `<table>...</table>`
		// doesn't end up wrapped in a stray <p>. We also treat any
		// `[[__...]]` placeholder as a block boundary — those are the
		// Phase-1p TOC markers that the post-wrap Phase 12 will expand
		// into a real <div>. Without this check the TOC ends up
		// wrapped in <p>, which produces `<p><div>…</div></p>` (the
		// browser auto-closes the <p> at the <div>, leaving the TOC
		// inside an orphan paragraph fragment).
		if trimmed == "" || blockOpenStart.MatchString(trimmed) || blockCloseStart.MatchString(trimmed) ||
			strings.HasPrefix(trimmed, "%%WRAP_BLOCK_") ||
			strings.HasPrefix(trimmed, "%%BLOCK_") ||
			strings.HasPrefix(trimmed, "[[__") {
			flush()
			result = append(result, line)
		} else {
			buf = append(buf, trimmed)
		}
	}
	flush()

	return strings.Join(result, "\n")
}

// avoid unused import warnings in build configs that strip the slog
// reference; called from the renderers that surface module errors.
