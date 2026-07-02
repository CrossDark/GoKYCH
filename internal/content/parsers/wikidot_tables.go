package parsers

import (
	"fmt"
	"html"
	"strings"
)

func renderWikidotTableRowLine(p *wikidotParser, line string, headerRow bool) string {
	line = strings.TrimSpace(line)
	// Strip leading/trailing `||`
	line = strings.TrimPrefix(line, "||")
	line = strings.TrimSuffix(line, "||")
	// Split on `||` and walk — consecutive empty splits indicate
	// a `||||` cell-separator, which the original parser rendered
	// as a single empty <th>/<td>. We keep that behaviour and
	// additionally emit `colspan="N"` so the table actually
	// collapses instead of leaving a visible empty column.
	raw := strings.Split(line, "||")
	var sb strings.Builder
	for i := 0; i < len(raw); i++ {
		// Count runs of empty cells ahead.
		colspan := 1
		for i+1 < len(raw) && strings.TrimSpace(raw[i+1]) == "" {
			colspan++
			i++
		}
		c := strings.TrimSpace(raw[i])
		isHeader := headerRow || strings.HasPrefix(c, "~")
		tag := "td"
		if isHeader {
			tag = "th"
			if c != "" {
				c = strings.TrimPrefix(c, "~")
				c = strings.TrimSpace(c)
			}
		}
		if colspan > 1 {
			sb.WriteString(fmt.Sprintf(`<%s colspan="%d">%s</%s>`, tag, colspan, renderWikidotCellInline(c), tag))
		} else {
			sb.WriteString(fmt.Sprintf(`<%s>%s</%s>`, tag, renderWikidotCellInline(c), tag))
		}
	}
	return sb.String()
}

// joinMultiLineTableRows is the pre-pass for table-row
// rendering. It walks the source line-by-line and, for each
// `||…` line that does NOT end with `||` on the same line
// (i.e. a multi-line row opener), collects subsequent lines
// until the matching line that DOES end with `||`. The
// joined cell content replaces each internal `\n` with a
// single space so the multi-line cell becomes one
// whitespace-separated cell.
//
// Wikidot's spec example:
//
//	|||||| 超长 _
//	内容 8||
//
// → after joinMultiLineTableRows:
//
//	|||||| 超长   内容 8||
//
// (the ` _\n` continuation marker is consumed; the
// newline becomes a single space so the cell still reads
// naturally without a hard break).
//
// Unmatched openers (no later line ending with `||`) are
// left verbatim so the author can see the typo. The
// function never produces input that would break a
// well-formed row, so the downstream renderWikidotTableRows
// pass can rely on each row fitting a single source line.
//
// Limitations:
//   - Only `||…` lines (not `[[table]]…[[/table]]` blocks)
//     are processed here — the [[table]] form already
//     accepts newlines inside cells.
//   - Inside a multi-line row the literal ` _\n` is the
//     continuation marker; without the marker (e.g. a bare
//     newline) the cell still joins, but a literal `_<space>\n`
//     in author prose outside the row is left alone.
func joinMultiLineTableRows(text string) string {
	lines := strings.Split(text, "\n")
	var sb strings.Builder
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		// Multi-line opener: starts with `||` but does NOT
		// end with `||`. A line that ends with `||` is a
		// single-line row and is left untouched (the
		// downstream pass handles it).
		if strings.HasPrefix(trimmed, "||") && !strings.HasSuffix(trimmed, "||") && len(trimmed) >= 2 {
			parts := []string{lines[i]}
			j := i + 1
			joined := false
			for j < len(lines) {
				next := strings.TrimSpace(lines[j])
				if strings.HasPrefix(next, "||") && strings.HasSuffix(next, "||") && len(next) >= 4 {
					parts = append(parts, lines[j])
					joined = true
					break
				}
				// Continuation line. We discard any `_ _`
				// marker at the end (the spec example uses
				// ` _\n` to signal continuation) and just
				// keep the trimmed text. The cells of a
				// joined row are separated by a single
				// space when the final row-line is
				// emitted.
				cleaned := strings.TrimRight(lines[j], " \t")
				cleaned = strings.TrimRight(cleaned, "_")
				cleaned = strings.TrimRight(cleaned, " \t")
				parts = append(parts, cleaned)
				j++
			}
			if joined {
				// Emit the joined line as a single
				// source line. We replace the source
				// newlines with `\n` so the row
				// pipeline sees one logical row;
				// cell-content newlines become literal
				// `\n` in the cell and the rendering
				// pipeline converts them to a space
				// when emitting <td> content.
				sb.WriteString(strings.Join(parts, " "))
			} else {
				// Unmatched opener — emit each line
				// verbatim so the author sees the
				// typo.
				for k, p := range parts {
					sb.WriteString(p)
					if k < len(parts)-1 {
						sb.WriteString("\n")
					}
				}
			}
			if i < len(lines)-1 || joined {
				// Only re-emit a trailing newline if
				// there are more lines after this
				// region. Parts already contain all
				// consumed lines; if `joined`, we've
				// consumed through `j`, which we
				// updated past. Loop's `i = j + 1`
				// below handles the index.
				_ = joined
			}
			if joined && i < len(lines)-1 {
				sb.WriteString("\n")
			}
			i = j + 1
			continue
		}
		sb.WriteString(lines[i])
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
		i++
	}
	return sb.String()
}

// renderWikidotTableRows finds contiguous runs of `||…||` lines and
// replaces each run with a single `<table>` placeholder. The first row
// is treated as the header row (Wikidot convention; if the first row's
// cells don't have `~` markers, they're still rendered as <th>).
//
// Before renderWikidotTableRows the source is run through
// joinMultiLineTableRows so multi-line cells (Wikidot's `_ _ \n`
// continuation marker inside a `||…||` block) collapse into a single
// row-line here.
func renderWikidotTableRows(p *wikidotParser, text string) string {
	lines := strings.Split(text, "\n")
	var result strings.Builder
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "||") && strings.HasSuffix(t, "||") && len(t) >= 4 {
			// Collect contiguous || lines.
			j := i
			for j < len(lines) {
				t2 := strings.TrimSpace(lines[j])
				if !(strings.HasPrefix(t2, "||") && strings.HasSuffix(t2, "||") && len(t2) >= 4) {
					break
				}
				j++
			}
			// lines[i:j] is a table.
			var sb strings.Builder
			sb.WriteString(`<table class="wiki-table"><tbody>`)
			for k, line := range lines[i:j] {
				// First row is the header unless none of its cells
				// actually use the `~` marker — in that case we still
				// promote them to <th> because Wikidot readers expect
				// the first row to be the header. Authors wanting a
				// body-only table can use [[table]]…[[/table]].
				isHeader := k == 0
				sb.WriteString("<tr>")
				sb.WriteString(renderWikidotTableRowLine(p, line, isHeader))
				sb.WriteString("</tr>")
			}
			sb.WriteString("</tbody></table>")
			result.WriteString(p.storeBlock(sb.String()))
			i = j
		} else {
			result.WriteString(lines[i])
			if i < len(lines)-1 {
				result.WriteString("\n")
			}
			i++
		}
	}
	return result.String()
}

// parseImageAttrs extracts `key="value"` pairs from the
// attribute tail of a `[[image ...]]` block. Returns an
// empty map for an empty tail. Keys are normalised to
// lowercase so callers can look them up case-insensitively.
func parseImageAttrs(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" {
		return out
	}
	for _, m := range reWDImageAttr.FindAllStringSubmatch(raw, -1) {
		out[strings.ToLower(m[1])] = m[2]
	}
	return out
}

// parseCollapsibleAttrs is the `[[collapsible ...]]`-block
// equivalent of parseImageAttrs. It pulls `show` / `hide` /
// `folded` / `hideLocation` out of the attribute tail; the
// regex is shared with the image parser, so a single
// key="value" pair works either way.
func parseCollapsibleAttrs(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" {
		return out
	}
	for _, m := range reWDCollapsibleAttr.FindAllStringSubmatch(raw, -1) {
		out[strings.ToLower(m[1])] = m[2]
	}
	return out
}

// buildImageTag composes the <img> tag from a sanitised
// source URL and a map of recognised attributes. We always
// emit `max-width:100%` so a wide source image doesn't blow
// out the article column — user-supplied `style` is
// prepended so the author can still override other props
// (the `max-width` is preserved by appending after the
// user's value).
func buildImageTag(src string, attrs map[string]string) string {
	var sb strings.Builder
	sb.WriteString(`<img src="`)
	sb.WriteString(src)
	sb.WriteString(`" alt="" loading="lazy"`)
	if w, ok := attrs["width"]; ok && w != "" {
		sb.WriteString(` width="`)
		sb.WriteString(html.EscapeString(w))
		sb.WriteString(`"`)
	}
	if h, ok := attrs["height"]; ok && h != "" {
		sb.WriteString(` height="`)
		sb.WriteString(html.EscapeString(h))
		sb.WriteString(`"`)
	}
	if cls, ok := attrs["class"]; ok && cls != "" {
		sb.WriteString(` class="`)
		sb.WriteString(sanitizeAnchorID(cls))
		sb.WriteString(`"`)
	}
	userStyle := ""
	if st, ok := attrs["style"]; ok {
		if css := sanitizeCSSValue(st); css != "" {
			userStyle = css
		}
	}
	if userStyle != "" {
		sb.WriteString(` style="`)
		sb.WriteString(userStyle)
		// sanitizeCSSValue accepts declarations with or
		// without a trailing `;` (it normalises by
		// appending one if missing). We add a space
		// before max-width so the two declarations
		// are visually separated regardless of how
		// the author wrote the trailing punctuation.
		sb.WriteString(` max-width:100%"`)
	} else {
		sb.WriteString(` style="max-width:100%"`)
	}
	sb.WriteString(`>`)
	return sb.String()
}

// renderImageWrapped composes the `<img>` tag (via
// buildImageTag) and, if the author supplied a `link`
// attribute, wraps the result in an `<a>` whose form depends
// on the attribute value (see the comments in the link
// handling below). The link semantics mirror what Wikidot
// does for `[[image … link="…"]]`:
//
//	link="*url"        external URL, opens in new tab
//	link="http(s)://"  external URL (no `*` prefix), opens in new tab
//	link="/path"       internal relative path
//	link="#anchor"     in-page anchor link
//	link="wiki-page"   slug → /wikidot/<slug>
//
// Link values that fail `sanitizeURLForAttr` fall through as
// a bare `<img>` so the author can see the typo.
func renderImageWrapped(src string, attrs map[string]string) string {
	img := buildImageTag(src, attrs)
	link, ok := attrs["link"]
	if !ok || link == "" {
		return img
	}
	// `*url` — strip the star and emit with new-tab attributes.
	if strings.HasPrefix(link, "*") {
		target := strings.TrimPrefix(link, "*")
		if safe := sanitizeURLForAttr(target); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, img)
		}
		return img
	}
	// `#anchor` — in-page anchor link. No new-tab.
	if strings.HasPrefix(link, "#") {
		anchor := strings.TrimPrefix(link, "#")
		// Sanitise the anchor id the same way as `[#name]` does:
		// HTML-escape, no further filtering (Wikidot anchor ids
		// accept Chinese / non-ASCII characters).
		if anchor == "" {
			return img
		}
		return fmt.Sprintf(`<a href="#%s">%s</a>`, html.EscapeString(anchor), img)
	}
	// `http://` / `https://` / `/path` (existing
	// sanitizeURLForAttr allow-list covers each case). Anything
	// else (e.g. `mailto:foo`) returns "" and we fall through
	// to a bare <img>. External http(s) URLs do NOT add
	// `rel="nofollow noopener" target="_blank"` here — that
	// is the contract for the `link="*url"` (starred) form
	// only. The bare `link="http://…"` form preserves the
	// historical behaviour (a plain wrap with no extras).
	if safe := sanitizeURLForAttr(link); safe != "" {
		return fmt.Sprintf(`<a href="%s">%s</a>`, safe, img)
	}
	// `wiki-page` (a bare slug, no `/wikidot/` prefix) is treated
	// as an internal wikidot page reference — same posture as the
	// `[1] / some-page` URL routing used by the article-detail
	// page (`/wikidot/<slug>`). The slug must be URL-safe: only
	// letters, digits, dash, underscore, dot, percent, and the
	// category-namespace `:` colon. Any other character (whitespace,
	// `/`, `?`, `#`, etc.) makes the slug ambiguous, so we fall
	// through to a bare `<img>` rather than guessing.
	if link != "" && !strings.ContainsAny(link, " \t\n/?#\"'<>") {
		slugSafe := true
		for _, c := range link {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_' || c == ':' ||
				c == '.' || c == '%') {
				slugSafe = false
				break
			}
		}
		if slugSafe {
			return fmt.Sprintf(`<a href="/wikidot/%s">%s</a>`, link, img)
		}
	}
	return img
}

// renderAlignedImage processes an `[[=image…]]` / `[[<image…]]` /
// `[[>image…]]` / `[[f<image…]]` / `[[f>image…]]` match. The
// regex captures (URL, attr-tail); we route both through the
// existing buildImageTag helper and wrap the result in a div
// carrying the alignment class so the front-end can position
// the image without needing extra CSS per article.
func renderAlignedImage(_ string, raw string, align string) string {
	// Pick the regex matching the alignment that's being
	// rendered. Each `render*` invocation is a closure over
	// one of the five prefix regexes, so we resolve the
	// match by repeating the same regex find here.
	m := reMatchAlign(raw, align)
	if m == nil {
		return raw
	}
	src := strings.TrimSpace(m[1])
	if src == "" {
		return raw
	}
	attrs := parseImageAttrs(m[2])
	if safe := sanitizeURLForAttr(src); safe != "" {
		img := renderImageWrapped(safe, attrs)
		return fmt.Sprintf(`<div class="wikidot-image-wrap wikidot-image-%s">%s</div>`, align, img)
	}
	return raw
}

// reMatchAlign returns the FindStringSubmatch result for the
// alignment-prefixed image whose alignment is `align` (one of
// "center", "left", "right", "floatleft", "floatright"). The
// calling closure in Phase 3d.5 captures the corresponding
// regex without having to pass it through `renderAlignedImage`
// as a parameter.
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
			// Inline-only — don't wrap cells in <p>.
			sb.WriteString(fmt.Sprintf("<%s>%s</%s>", tag, inlineOnly(c), tag))
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</tbody></table>")
	return sb.String()
}

// ── inlineOnly ─────────────────────────────────────────────────────────

// inlineVars is the package-level shadow of the current
// render's `%%var%%` substitution table. The wikidotParser sets
// it in convert() before any inline pass and clears it after —
// inlineOnly then reads it without the parser-instance plumbing
// (which would otherwise force every list/table/span callback
// to thread the parser pointer through). The shadow is
// protected by a mutex so concurrent renders (each with their
// own parser instance) don't trample each other. Cost: one
// mutex acquire per inline pass per render — negligible
// against the surrounding regex work.
func renderWikidotCellInline(text string) string {
	// Smart-punctuation pass RUNS HERE because cell content is
	// stashed into a table-block at Phase 1e (BEFORE Phase 1d.7's
	// original smart-punct location, and now before Phase 8.5
	// which lives after Phase 8's blockquote processing). Without
	// this local pass, backticks / em-dashes / ellipsis inside `||`
	// cells would stay as ASCII characters because the outer
	// smart-punct passes see only `%%BLOCK_N%%` placeholders.
	//
	// The cell-scope pass uses the same regexes the outer pass
	// uses (Phase 8.5). Order matches: pairs run before singleton
	// halves so German `,,…''` consumes the closing `''` first.
	text = reWDSmartGerman.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDSmartGerman.FindStringSubmatch(s)
		return "\u201e" + m[1] + "\u201c"
	})
	text = reWDSmartLDQuote.ReplaceAllString(text, "\u201c")
	text = reWDSmartRDQuote.ReplaceAllString(text, "\u201d")
	text = reWDSmartLSQuote.ReplaceAllString(text, "\u2018")
	text = reWDSmartRSQuote.ReplaceAllString(text, "$1\u2019")
	text = reWDSmartLAQuote.ReplaceAllString(text, "\u00ab")
	text = reWDSmartRGTQuote.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDSmartRGTQuote.FindStringSubmatch(s)
		return "\u00bb" + m[1] + "\u00ab"
	})
	text = reWDSmartRAQuote.ReplaceAllString(text, "\u00bb")
	text = reWDEllipsis.ReplaceAllString(text, "\u2026")
	text = reWDEmDash.ReplaceAllString(text, "$1\u2014$2")

	// Reader comment: var substitution first so `%%x%%` survives a
	// later size/span wrapping.
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

	// Inline comments — same regex as Phase 0.65. We run BEFORE all
	// the wrapping passes so a `[!-- … --]` doesn't leak the inner
	// markers into a span that swallows the dash-dash as strike.
	text = reWDComment.ReplaceAllString(text, "")

	// Make sure nested [[span class="…"]] ruby/rt structures emit
	// valid HTML before any of the simpler inline regexes fire.
	// renderBalancedSpanClass is a function (not a package-level
	// regex) and internally does fixed-point expansion + mapSpanClassToElement;
	// it's safe to call here.
	text = renderBalancedSpans(text)

	// `[[size X]]Y[[/size]]` → style on the inner span. The body
	// version of this (renderWikidotSizeBlocks) is a *block-level*
	// pass because `[[size]]…[[/size]]` can legitimately span
	// paragraphs (`renderWikidotSizeBlocks` bodyRendered split).
	// Inside a TD we don't want paragraph wrapping, so we run a
	// local regex using the same value→css mapping.
	text = reWDSize.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDSize.FindStringSubmatch(s)
		css, ok := resolveSizeCss(m[1])
		if !ok {
			return inlineOnly(m[2])
		}
		return fmt.Sprintf(`<span style="font-size:%s">%s</span>`, css, inlineOnly(m[2]))
	})

	// Inline anchor-def `[[# name]]` — produces `<span id="…" class="wiki-anchor">`.
	// Same regex as Phase 1o2.5, run here so a `#name` hash anchor
	// inside a TD also lands.
	text = reWDHashAnchorDef.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDHashAnchorDef.FindStringSubmatch(s)
		id := html.EscapeString(strings.TrimSpace(m[1]))
		return fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, id)
	})

	// Hash anchor jump `[# name text]` — same regex the body
	// uses (reWDAnchor). The TD path has no other place to
	// realise this since `inlineOnly` doesn't include it.
	text = reWDAnchor.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDAnchor.FindStringSubmatch(s)
		anchor := strings.TrimSpace(m[1])
		display := strings.TrimSpace(m[2])
		if display == "" {
			display = anchor
		}
		return fmt.Sprintf(`<a href="#%s">%s</a>`, html.EscapeString(anchor), html.EscapeString(display))
	})

	// Empty placeholders `[# display]` → `<a href="javascript:;">display</a>`.
	// (Phase 3c.7 only runs on non-block-stashed text, so cells miss this.)
	text = reWDEmptyLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDEmptyLink.FindStringSubmatch(s)
		display := strings.TrimSpace(m[1])
		return fmt.Sprintf(`<a href="javascript:;">%s</a>`, html.EscapeString(display))
	})

	// Everything below mirrors inlineOnly so the inside of any
	// generated span / size also gets bold/italic/etc.
	return inlineOnly(text)
}

// ── blockquote / list rendering ─────────────────────────────────────────

// renderWikidotAdvancedLists walks the source for
// `[[ul]]` / `[[ol]]` / `[[li]]` / `[[/li]]` / `[[/ul]]`
// / `[[/ol]]` blocks, stashes the rendered HTML as a
// single block placeholder, and returns the source with
// each match replaced. We use a single FindAllSubmatch
// pass to collect every open/close token (with positions),
// then a stack-based walker to emit well-formed nested
// `<ul>`/`<ol>` HTML. Attributes (`class`, `style`,
// `data-...`) are forwarded from the open-tag tail.
//
// The walker is robust to author errors (unclosed
// `[[ul]]` is closed implicitly at the end of the
// matched region; lone `[[/li]]` is ignored). Nested
// `[[ul]]` / `[[ol]]` inside a `[[li]]` body is fully
// supported.
