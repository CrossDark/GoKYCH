package parsers

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
)

// ── Wikidot regexes ────────────────────────────────────────────────────
//
// Coverage matches what authors actually write on the site, not the full
// Wikidot spec (modules / include / page variables stay as raw source on
// purpose — those are server-side dynamic and the page already notes
// where they're headed). Block patterns are stored as placeholders so the
// inner content doesn't get re-processed by inline passes.

var (
	reWDCode       = regexp.MustCompile(`(?is)\[\[code(?:\s+type\s*=\s*['"]([^'"]+)['"])?\]\](.*?)\[\[/code\]\]`)
	reWDCodeMarker = regexp.MustCompile(`(?is)\[\[code(?:\s+type\s*=\s*['"]([^'"]+)['"])?\]\](.*?)\[\[/code\]\]`)
	// [[[wikidot link]]] is matched in Phase 3 (links) alongside
	// [url text] and [mailto:…] external link forms.

	reWDDiv       = regexp.MustCompile(`(?is)\[\[div\s+class="([^"]*)"\]\](.*?)\[\[/div\]\]`)
	reWDDivStyle  = regexp.MustCompile(`(?is)\[\[div\s+style="([^"]*)"\]\](.*?)\[\[/div\]\]`)
	reWDDivFloat  = regexp.MustCompile(`(?is)\[\[float\s*=\s*(left|right)\s*\]\](.*?)\[\[/float\]\]`)
	reWDTable     = regexp.MustCompile(`(?is)\[\[table\]\](.*?)\[\[/table\]\]`)
	// Row-based table syntax: lines starting with `||` and ending with `||`.
	// Each `||` is a cell separator; `||~ header ~||` denotes a header cell.
	// Merged-cell syntax `|| ||` (empty) or `||||` collapses to one cell.
	reWDTableRowLine = regexp.MustCompile(`(?m)^\s*\|\|[^|\n]*?(?:\|\|[^|\n]*?)*\|\|\s*$`)

	reWDSpanClass = regexp.MustCompile(`(?is)\[\[span\s+class="([^"]*)"\]\](.*?)\[\[/span\]\]`)
	reWDSpanStyle = regexp.MustCompile(`(?is)\[\[span\s+style="([^"]*)"\]\](.*?)\[\[/span\]\]`)
	reWDCollapsible = regexp.MustCompile(`(?is)\[\[collapsible\s+show="([^"]*)"\s+hide="([^"]*)"\]\](.*?)\[\[/collapsible\]\]`)
	reWDSize        = regexp.MustCompile(`(?is)\[\[size\s+([^\]]+)\]\](.*?)\[\[/size\]\]`)
	reWDColor       = regexp.MustCompile(`(?is)\[\[color\s+([^\]]+)\]\](.*?)\[\[/color\]\]`)
	reWDMath        = regexp.MustCompile(`(?is)\[\[math\]\](.*?)\[\[/math\]\]`)
	reWDHTMLRaw     = regexp.MustCompile(`(?is)\[\[html\]\](.*?)\[\[/html\]\]`)
	reWDYoutube     = regexp.MustCompile(`(?i)\[\[youtube\s+([A-Za-z0-9_-]{6,20})\]\]`)
	reWDAnchorDef   = regexp.MustCompile(`(?i)\[\[a\s+name\s*=\s*"?([^"\]]+?)"?\s*\]\]`)
	// Paired form: `[[a name="x"]]content[[/a]]` — wraps the inner block
	// in a span with id="x" so the [#x text] jump-link below can land
	// on it. Same anchor id rules as reWDAnchorDef (HTML-escape the id;
	// we don't constrain to ASCII because the test doc uses Chinese ids).
	reWDAnchorPair = regexp.MustCompile(`(?is)\[\[a\s+name\s*=\s*"?([^"\]]+?)"?\s*\]\](.*?)\[\[/a\]\]`)

	reWDCenter  = regexp.MustCompile(`(?s)\[\[=\]\](.*?)\[\[/=\]\]`)
	reWDRight   = regexp.MustCompile(`(?s)\[\[>\]\](.*?)\[\[/>\]\]`)
	reWDJustify = regexp.MustCompile(`(?s)\[\[==\]\](.*?)\[\[/==\]\]`)

	// Inline formatting (Phase 2). Bold/italic/underline/etc. kept verbatim
	// from the original parser; new additions below.
	reWDSuperscript   = regexp.MustCompile(`\^\^(.+?)\^\^`)
	reWDSubscript     = regexp.MustCompile(`,,(.+?),,`)
	reWDAutoURL = regexp.MustCompile(`(?i)\b(https?://[^\s<>\[\]]+)`)
	reWDLineBreak  = regexp.MustCompile(`(?i)\[\[br\]\]`)
	// Jump-link `[#name]` or `[#name text]` (Wikidot uses SINGLE
	// brackets here, unlike the `[[a name=…]]` anchor def above).
	// When text is present, emit a clickable anchor that scrolls to
	// the matching id="name" span emitted by reWDAnchorDef/Pair;
	// without text, fall back to a self-anchor span (rare — used to
	// drop an anchor into a position the author can reference from
	// elsewhere).
	//
	// Note: no `\s+` between `#` and the name — Wikidot uses `[#x]`
	// without a space, but `[#x alias text]` has a space before the
	// alias. Treat the boundary as "non-`]` non-whitespace" so the
	// name capture is greedy.
	reWDAnchor = regexp.MustCompile(`\[#([^\]\s]+)(?:\s+([^\]]+))?\]`)
	reWDMono          = regexp.MustCompile(`@@([^@]+?)@@`)
	reWDBold          = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Italic `//x//` — the opening `//` must NOT be preceded by `:`,
	// so URLs like `https://example.com` (which contain `://`) don't
	// false-positive when they appear inside HTML tags the auto-linker
	// produced. The first capture holds the safe prefix (`^` or a
	// non-`:` char) which the replace helper re-emits unchanged.
	reWDItalic = regexp.MustCompile(`(?m)(^|[^:])//(.+?)//`)
	reWDUnderline     = regexp.MustCompile(`__(.+?)__`)
	reWDStrikethrough = regexp.MustCompile(`--(.+?)--`)
	reWDInlineCode    = regexp.MustCompile(`\{\{(.+?)\}\}`)
	// Inline colour in the Wikidot "##color|text##" form (no brackets).
	// Must run before the bold/italic passes — `**` and `##` are
	// syntactically unrelated, but processing colour early keeps the
	// pipeline simple.
	reWDInlineColor = regexp.MustCompile(`##([A-Za-z]+)\|([^#]+)##`)

	// Phase 3 links — external [url text] and mailto [mailto:addr text].
	// Wikidot's internal link form `[[[page|alias]]]` is below.
	reWDExternalLink = regexp.MustCompile(`\[(https?://[^\s\]]+)(?:\s+([^\]]+))?\]`)
	reWDMailto       = regexp.MustCompile(`\[mailto:([^\s\]]+)(?:\s+([^\]]+))?\]`)
	reWDWikiLink     = regexp.MustCompile(`\[\[\[([^\]]+?)(?:\s*\|\s*([^\]]+?))?\]\]\]`)
	// [[image URL]] and [[image URL link="..."]] — the optional link=
	// attribute wraps the <img> in an <a>. The first capture is the URL,
	// the (optional) second is the link target.
	reWDImage = regexp.MustCompile(`(?i)\[\[image\s+([^\s\]]+)(?:\s+link\s*=\s*"([^"]+)")?\s*\]\]`)

	reWDH6_ = regexp.MustCompile(`(?m)^\+\+\+\+\+\+\s+(.+)$`)
	reWDH5_ = regexp.MustCompile(`(?m)^\+\+\+\+\+\s+(.+)$`)
	reWDH4_ = regexp.MustCompile(`(?m)^\+\+\+\+\s+(.+)$`)
	reWDH3_ = regexp.MustCompile(`(?m)^\+\+\+\s+(.+)$`)
	reWDH2_ = regexp.MustCompile(`(?m)^\+\+\s+(.+)$`)
	reWDH1_ = regexp.MustCompile(`(?m)^\+\s+(.+)$`)

	reWDBlockquote    = regexp.MustCompile(`(?m)^(?:&gt;|>)\s?(.*)$`)
	reWDUnorderedItem = regexp.MustCompile(`(?m)^(\s*)\*\s+(.+)$`)
	reWDOrderedItem   = regexp.MustCompile(`(?m)^(\s*)#\s+(.+)$`)
	reWDHR            = regexp.MustCompile(`(?m)^-{4,}$`)
	reWDAdmonition    = regexp.MustCompile(`(?sm)^!!!\s+(note|warning|danger|info|tip)\s*\n(.*?)(\n!!!|\n\[\[|\z)`)
	reWDHTMLBlock     = regexp.MustCompile(`(?s)(<(?:pre|table|ul|ol|blockquote|div|details|summary)\b.*?</(?:pre|table|ul|ol|blockquote|div|details|summary)>)`)
)

// ── Size / color lookup tables ─────────────────────────────────────────

var sizeMap = map[string]string{
	"xx-small": "0.5rem", "x-small": "0.625rem", "smaller": "0.75rem",
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

// ── WikidotParser (singleton, pooled for thread safety) ────────────────

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

	// ── Phase 1: block storage ────────────────────────────────────────
	// The order matters: anything that can contain `]]` later in the
	// content (math, code, div, html) has to be stashed before patterns
	// that match raw HTML, otherwise the placeholder markers would be
	// wrapped by `<p>`.

	// 1a. [[html]] — kept raw on purpose; this is how we refuse the
	// "let authors paste iframe embeds" footgun. Render the content
	// inside an escaped <pre> so an admin can still see what the
	// source actually said (and rip out the wrapper if they really
	// want it).
	out = reWDHTMLRaw.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDHTMLRaw.FindStringSubmatch(s)
		return p.storeBlock(fmt.Sprintf(`<pre class="wikidot-html-escaped">&lt;html&gt;\n%s\n&lt;/html&gt;</pre>`, html.EscapeString(strings.TrimSpace(m[1]))))
	})

	// 1b. Math — keep LaTeX source verbatim, wrap in delimiters so a
	// future MathJax/KaTeX script can replace them client-side.
	out = reWDMath.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDMath.FindStringSubmatch(s)
		return p.storeBlock(fmt.Sprintf(`<div class="wikidot-math">\(%s\)</div>`, strings.TrimSpace(m[1])))
	})

	// 1c. [[code]] blocks
	out = reWDCode.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCode.FindStringSubmatch(s)
		return p.storeBlock(renderCodeBlock(m[2], m[1]))
	})

	// 1d. Collapsible sections
	out = reWDCollapsible.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCollapsible.FindStringSubmatch(s)
		inner := p.convert(m[3])
		return p.storeBlock(fmt.Sprintf(`<details class="wiki-collapsible"><summary>%s</summary><div class="collapsible-content">%s</div></details>`, m[1], inner))
	})

	// 1e. Row-based tables (`|| ... ||` lines, contiguous group). Build
	// the table HTML and stash it so subsequent regex passes don't try
	// to interpret `|` characters or re-parse the cell content.
	out = renderWikidotTableRows(p, out)

	// 1f. [[table]]...[[/table]] block syntax
	out = reWDTable.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDTable.FindStringSubmatch(s)
		return p.storeBlock(renderWikidotTable(p, m[1]))
	})

	// 1g. [[div class=…]] and [[div style=…]]
	out = reWDDiv.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDDiv.FindStringSubmatch(s)
		inner := p.convert(m[2])
		cls := sanitizeAnchorID(m[1])
		return p.storeBlock(fmt.Sprintf(`<div class="%s">%s</div>`, cls, inner))
	})
	out = reWDDivStyle.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDDivStyle.FindStringSubmatch(s)
		inner := p.convert(m[2])
		if css := sanitizeCSSValue(m[1]); css != "" {
			return p.storeBlock(fmt.Sprintf(`<div style="%s">%s</div>`, css, inner))
		}
		return inner
	})

	// 1h. [[float=left|right]] — wrap content in a floating div
	out = reWDDivFloat.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDDivFloat.FindStringSubmatch(s)
		inner := p.convert(m[2])
		side := strings.ToLower(strings.TrimSpace(m[1]))
		if side != "left" && side != "right" {
			side = "left"
		}
		return p.storeBlock(fmt.Sprintf(`<div style="float:%s">%s</div>`, side, inner))
	})

	// 1i. [[span class=…]] / [[span style=…]] — inline only, no block
	// patterns or paragraph wrapping (span is inline; nesting <p> inside
	// it is invalid HTML).
	out = reWDSpanClass.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSpanClass.FindStringSubmatch(s)
		inner := inlineOnly(m[2])
		if cls := sanitizeAnchorID(m[1]); cls != "" {
			return fmt.Sprintf(`<span class="%s">%s</span>`, cls, inner)
		}
		return inner
	})
	out = reWDSpanStyle.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSpanStyle.FindStringSubmatch(s)
		inner := inlineOnly(m[2])
		if css := sanitizeCSSValue(m[1]); css != "" {
			return fmt.Sprintf(`<span style="%s">%s</span>`, css, inner)
		}
		return inner
	})

	// 1j. [[size]] / [[color]]
	out = reWDSize.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSize.FindStringSubmatch(s)
		css := m[1]
		if v, ok := sizeMap[strings.ToLower(css)]; ok {
			css = v
		} else if css = sanitizeCSSValue(css); css == "" {
			return inlineOnly(m[2])
		}
		return fmt.Sprintf(`<span style="font-size:%s">%s</span>`, css, inlineOnly(m[2]))
	})
	out = reWDColor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDColor.FindStringSubmatch(s)
		css := m[1]
		if v, ok := colorNames[strings.ToLower(css)]; ok {
			css = v
		} else if css = sanitizeCSSValue(css); css == "" {
			return inlineOnly(m[2])
		}
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, inlineOnly(m[2]))
	})

	// 1k. Alignment blocks ([[=]] / [[>]] / [[==]])
	out = reWDCenter.ReplaceAllString(out, `<div style="text-align:center">$1</div>`)
	out = reWDRight.ReplaceAllString(out, `<div style="text-align:right">$1</div>`)
	out = reWDJustify.ReplaceAllString(out, `<div style="text-align:justify">$1</div>`)

	// 1l. [[youtube ID]] — embed iframe, validate ID length before
	// emitting (otherwise `[[youtube ]]` or `[[youtube …/alert(1)…]]`
	// could produce a malformed iframe src).
	out = reWDYoutube.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDYoutube.FindStringSubmatch(s)
		return p.storeBlock(fmt.Sprintf(`<div class="wikidot-youtube"><iframe src="https://www.youtube.com/embed/%s" loading="lazy" allowfullscreen frameborder="0" referrerpolicy="strict-origin-when-cross-origin"></iframe></div>`, html.EscapeString(m[1])))
	})

	// 1m. Paired anchor block `[[a name="x"]]content[[/a]]` runs
	// FIRST so the non-greedy `.*?` between opening and `[[/a]]`
	// closing can capture the inner content before the simpler
	// self-closing `reWDAnchorDef` below eats just the opening tag.
	//
	// We emit the id-bearing span as an empty placeholder *before* the
	// (recursively converted) content rather than wrapping content in
	// the span. Wrapping would nest <p> (from paragraph wrapping) inside
	// the inline <span>, which is invalid HTML and ends up visually
	// broken in browsers. Anchoring an empty span right above the
	// content still gives the jump-link `[[#x text]]` the correct
	// scroll target.
	out = reWDAnchorPair.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAnchorPair.FindStringSubmatch(s)
		id := html.EscapeString(strings.TrimSpace(m[1]))
		inner := p.convert(m[2])
		return p.storeBlock(fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, id)) + inner
	})

	// 1m2. Self-closing `[[a name="…"]]` (no `[[/a]]`). Stored as a
	// placeholder so the regex that resolves [#name] jumps later
	// doesn't try to also fire on this line.
	out = reWDAnchorDef.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAnchorDef.FindStringSubmatch(s)
		// Anchor IDs are commonly Chinese or other non-ASCII; fall
		// back to a permissive escape rather than dropping the
		// anchor entirely. sanitizeAnchorID is too strict for IDs
		// the author genuinely wants to use.
		id := strings.TrimSpace(m[1])
		id = html.EscapeString(id)
		return p.storeBlock(fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, id))
	})

	// ── Phase 2: inline formatting ───────────────────────────────────
	// Pre-process: replace backslash-escaped slashes with a sentinel
	// so `//` isn't confused with italic markers.
	out = strings.ReplaceAll(out, `\\/`, "\x00SL")

	// 2a. @@mono@@ — Wikidot convention is monospace (was mis-labelled
	// as "escape" in the previous pipeline; raw < > in user content are
	// already caught by the client's DOMPurify pass).
	out = reWDMono.ReplaceAllString(out, `<code>$1</code>`)

	// 2b. Inline colour `##colorname|text##`
	out = reWDInlineColor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDInlineColor.FindStringSubmatch(s)
		name := strings.ToLower(strings.TrimSpace(m[1]))
		text := m[2]
		// Inline `##...##` syntax only accepts the named colour
		// lookup table — we don't fall back to raw CSS values
		// (use `[[color name]]text[[/color]]` for arbitrary CSS).
		// This keeps `##greyish|...##` from silently rendering as
		// `style="color:greyish"` (invalid CSS, but allowed by the
		// generic char whitelist).
		css, ok := colorNames[name]
		if !ok {
			return text
		}
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, html.EscapeString(text))
	})

	// 2c. The standard formatting stack.
	out = reWDBold.ReplaceAllString(out, `<strong>$1</strong>`)
	// Italic regex requires the opening `//` to NOT be preceded by `:` —
	// otherwise the `://` inside `https://example.com` URLs (which are
	// already wrapped by the Phase 6 auto-linker into <a> tags when the
	// list / table / span passes call inlineOnly later) trips the
	// match and inserts `<em>` in the middle of an <a href="...">.
	out = reWDItalic.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDItalic.FindStringSubmatch(s)
		return m[1] + "<em>" + m[2] + "</em>"
	})
	out = reWDUnderline.ReplaceAllString(out, `<u>$1</u>`)
	out = reWDStrikethrough.ReplaceAllString(out, `<s>$1</s>`)
	out = reWDSuperscript.ReplaceAllString(out, `<sup>$1</sup>`)
	out = reWDSubscript.ReplaceAllString(out, `<sub>$1</sub>`)
	out = reWDInlineCode.ReplaceAllString(out, `<code>$1</code>`)
	if strings.Contains(out, "直接URL") {
	}

	out = strings.ReplaceAll(out, "\x00SL", "/")

	// ── Phase 3: links & images ──────────────────────────────────────
	// 3a. External link `[url text]` (open in new tab; rel=noopener
	// is the safe default for user-authored content).
	out = reWDExternalLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDExternalLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = url
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3b. Mailto `[mailto:addr text]`
	out = reWDMailto.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDMailto.FindStringSubmatch(s)
		addr := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = addr
		}
		if safe := sanitizeURLForAttr("mailto:" + addr); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3c. Internal wiki link `[[[page]]]` / `[[[page|alias]]]`
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
		if safe := sanitizeURLForAttr(href); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3d. Image — optional `link="…"` attribute wraps the <img>.
	out = reWDImage.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDImage.FindStringSubmatch(s)
		src := strings.TrimSpace(m[1])
		link := strings.TrimSpace(m[2])
		if safe := sanitizeURLForAttr(src); safe != "" {
			img := fmt.Sprintf(`<img src="%s" alt="" loading="lazy" style="max-width:100%%">`, safe)
			if linkSafe := sanitizeURLForAttr(link); linkSafe != "" {
				return fmt.Sprintf(`<a href="%s">%s</a>`, linkSafe, img)
			}
			return img
		}
		return ""
	})

	// ── Phase 4: headings ────────────────────────────────────────────
	// Order longest-prefix first so `++++++` is consumed before `+++++`
	// before `++++` (regex leftmost-longest match would handle this,
	// but explicit ordering keeps the pipeline readable).
	out = reWDH6_.ReplaceAllString(out, `<h6>$1</h6>`)
	out = reWDH5_.ReplaceAllString(out, `<h5>$1</h5>`)
	out = reWDH4_.ReplaceAllString(out, `<h4>$1</h4>`)
	out = reWDH3_.ReplaceAllString(out, `<h3>$1</h3>`)
	out = reWDH2_.ReplaceAllString(out, `<h2>$1</h2>`)
	out = reWDH1_.ReplaceAllString(out, `<h1>$1</h1>`)

	// ── Phase 5: horizontal rules ────────────────────────────────────
	out = reWDHR.ReplaceAllString(out, `<hr>`)

	// ── Phase 6: line breaks & jump-anchor links & auto-URLs ─────────
	out = reWDLineBreak.ReplaceAllString(out, `<br>`)
	if strings.Contains(out, "直接URL") {
	}
	// Auto-link bare URLs (Wikidot's default behaviour). Runs after the
	// explicit `[url text]` form so a hand-formatted link isn't double-
	// wrapped. The regex excludes `<`, `>`, `[`, `]` to avoid eating
	// into adjacent HTML or wikidot link syntax; trailing punctuation
	// like `,` or `.` may end up inside the href — that's harmless on
	// the link's click target and gets cleaned by the DOMPurify pass
	// on the client.
	out = reWDAutoURL.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAutoURL.FindStringSubmatch(s)
		url := m[1]
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, safe)
		}
		return s
	})
	out = reWDAnchor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAnchor.FindStringSubmatch(s)
		name := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			// `[#name]` with no text — emit an empty anchor span so
			// it's a valid drop-in target for any cross-reference.
			return fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, html.EscapeString(name))
		}
		return fmt.Sprintf(`<a href="#%s" class="wiki-anchor-link">%s</a>`, html.EscapeString(name), html.EscapeString(text))
	})
	if strings.Contains(out, "直接URL") {
	}

	// ── Phase 7: admonitions ─────────────────────────────────────────
	if strings.Contains(out, "直接URL") {
	}
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

	// ── Phase 8: blockquotes ─────────────────────────────────────────
	out = renderWikidotBlockquotes(out)

	// ── Phase 9: lists ───────────────────────────────────────────────
	out = renderWikidotLists(out)

	// ── Phase 10: restore stored blocks ──────────────────────────────
	if strings.Contains(out, "直接URL") {
	}
	for key, blk := range p.blocks {
		out = strings.ReplaceAll(out, key, blk)
	}

	// ── Phase 11: paragraph wrapping ─────────────────────────────────
	out = wrapWikidotParagraphs(out)

	return out
}

// ── Helper renderers ────────────────────────────────────────────────────

func renderCodeBlock(code, lang string) string {
	c := html.EscapeString(code)
	cls := ""
	if lang != "" {
		cls = fmt.Sprintf(` class="language-%s"`, lang)
	}
	return fmt.Sprintf(`<pre><code%s>%s</code></pre>`, cls, c)
}

// renderWikidotTableRowLine parses one `||…||` line into cells. A cell
// starting with `~` is a header cell. Empty cells (from `|| ||` or
// `||||` merges) are preserved as empty strings.
//
// Inline-only on cell content so <p> doesn't end up inside <td>/<th>.
// Authors wanting block content inside a cell should use the
// [[table]]…[[/table]] block syntax with HTML authored directly.
func renderWikidotTableRowLine(p *wikidotParser, line string, headerRow bool) string {
	line = strings.TrimSpace(line)
	// Strip leading/trailing `||`
	line = strings.TrimPrefix(line, "||")
	line = strings.TrimSuffix(line, "||")
	cells := strings.Split(line, "||")
	var sb strings.Builder
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if headerRow || strings.HasPrefix(c, "~") {
			headerRow = true
			c = strings.TrimPrefix(c, "~")
			c = strings.TrimSpace(c)
			sb.WriteString("<th>")
			sb.WriteString(inlineOnly(c))
			sb.WriteString("</th>")
		} else {
			sb.WriteString("<td>")
			sb.WriteString(inlineOnly(c))
			sb.WriteString("</td>")
		}
	}
	return sb.String()
}

// renderWikidotTableRows finds contiguous runs of `||…||` lines and
// replaces each run with a single `<table>` placeholder. The first row
// is treated as the header row (Wikidot convention; if the first row's
// cells don't have `~` markers, they're still rendered as <th>).
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

// inlineOnly applies inline formatting to text (no block elements).
func inlineOnly(text string) string {
	text = reWDMono.ReplaceAllString(text, `<code>$1</code>`)
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
	text = reWDInlineColor.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDInlineColor.FindStringSubmatch(s)
		name := strings.ToLower(strings.TrimSpace(m[1]))
		text := m[2]
		css, ok := colorNames[name]
		if !ok {
			return text
		}
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, html.EscapeString(text))
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
	text = reWDWikiLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		href := m[1]
		text := m[1]
		if m[2] != "" {
			text = m[2]
		}
		if !strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			href = "/wikidot/" + href
		}
		if safe := sanitizeURLForAttr(href); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
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
		// Treat placeholder markers as block boundaries so a placeholder
		// that resolves to e.g. `<span>...</span>` or `<table>...</table>`
		// doesn't end up wrapped in a stray <p>.
		if trimmed == "" || blockTagStart.MatchString(trimmed) ||
			strings.HasPrefix(trimmed, "%%WRAP_BLOCK_") ||
			strings.HasPrefix(trimmed, "%%BLOCK_") {
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
