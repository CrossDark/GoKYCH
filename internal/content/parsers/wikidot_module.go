package parsers

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

func parseModuleAttrs(raw string) map[string]string {
	out := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	// Strip a leading `|` so `category="*"|limit=5` parses the
	// same as `category="*" limit=5`.
	raw = strings.TrimLeft(raw, "|")
	// Tokenize on `|` (Wikidot's official separator) OR on
	// whitespace — both are seen in the wild.
	tokens := splitModuleTokens(raw)
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		eq := strings.Index(t, "=")
		if eq < 0 {
			out[strings.ToLower(t)] = ""
			continue
		}
		k := strings.ToLower(strings.TrimSpace(t[:eq]))
		v := strings.TrimSpace(t[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}

func splitModuleTokens(s string) []string {
	// Walk character by character to keep `key="value with space"`
	// intact. The `|`-separator is only a token boundary when it's
	// NOT inside quotes.
	var (
		tokens []string
		buf    strings.Builder
		inQ    bool
		qCh    byte
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQ:
			buf.WriteByte(c)
			if c == qCh {
				inQ = false
			}
		case c == '"' || c == '\'':
			inQ = true
			qCh = c
			buf.WriteByte(c)
		case c == '|':
			tokens = append(tokens, buf.String())
			buf.Reset()
		case c == ' ' || c == '\t':
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteByte(c)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

// parseIncludeAttrs is the same idea as parseModuleAttrs but for
// `[[include slug | k=v | k="v"]]`. The slug itself is the first
// token in the include regex; the attrs start at the `|` separator.
func parseIncludeAttrs(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "|") {
		return map[string]string{}
	}
	return parseModuleAttrs(raw[1:])
}

// renderTOC replaces `[[__TOC__]]` / `[[__FTOC_LEFT__]]` /
// `[[__FTOC_RIGHT__]]` placeholders (stored in Phase 1p) with
// a nested <ul> built from the headings collected during
// Phase 4. If no headings were collected, the toc placeholder
// is left as-is so the author can see why it's empty.
//
// `[[f<toc]]` / `[[f>toc]]` produce a floated version of the
// toc — the outer div has an extra `float:left` /
// `float:right` class so the surrounding text wraps around
// the contents list.
//
// Structure: a real `<ul>`-in-`<ul>` nesting reflects heading
// hierarchy (h2 outer, h3 nested inside its parent h2, …).
// The previous implementation was a flat `<ul>` that
// delegated indent to a CSS `.toc-h3 { padding-left: … }`
// rule; nested HTML is more readable in devtools, copies
// cleanly when users grab the markup for their own pages,
// and survives user-style-stripping (some browsers / reader
// modes drop CSS but keep element semantics). The CSS rule
// was kept as a fallback for the `[[toc]]` no-block case so
// the visual layout matches the old behaviour if a stylesheet
// override is active.
//
// Output shape (sample):
//
//	<div class="wikidot-toc" role="navigation" aria-label="Table of contents">
//	  <div class="wikidot-toc-title">Table of Contents</div>
//	  <ul class="wikidot-toc-list">
//	    <li class="toc-li toc-h2">
//	      <a href="#h2-1" class="toc-link toc-link-h2"><span class="toc-text">H2-a</span></a>
//	      <ul>
//	        <li class="toc-li toc-h3">
//	          <a href="#h3-1" class="toc-link toc-link-h3"><span class="toc-text">H3-a</span></a>
//	        </li>
//	      </ul>
//	    </li>
//	  </ul>
//	</div>
func renderWikidotTOCList(headings []headingEntry, minTocLevel int) string {
	tree := buildWikidotTOCTree(headings, minTocLevel)
	var sb strings.Builder
	sb.WriteString(`<ul class="wikidot-toc-list">`)
	sb.WriteString(renderTOCChildren(tree))
	sb.WriteString(`</ul>`)
	return sb.String()
}

// tocNode is the in-memory TOC tree. A nil slice of children
// means a leaf; rendering for a leaf emits a self-contained
// `<li>` without a nested `<ul>`.
func buildWikidotTOCTree(headings []headingEntry, minTocLevel int) *tocNode {
	if minTocLevel < 1 {
		minTocLevel = 1
	}
	root := &tocNode{} // dummy; children are real toc entries.
	stack := []*tocNode{root}
	for _, h := range headings {
		if h.SkipTOC {
			continue
		}
		if h.Level < minTocLevel || h.Level > 6 {
			continue
		}
		// Pop until top has level < h.Level (strictly
		// shallower). Edge case: an H4 following an H2
		// (without an H3 between) just attaches to the
		// H2's children — no synthetic intermediate level.
		for len(stack) > 1 && stack[len(stack)-1].heading.Level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		node := &tocNode{heading: h}
		top := stack[len(stack)-1]
		top.children = append(top.children, node)
		stack = append(stack, node)
	}
	return root
}

// renderTOCChildren serialises the children of `node` into a
// nested `<ul>`. The caller is responsible for wrapping this
// in the outer `<ul class="wikidot-toc-list">` (or for a
// recursion-level inner `<ul>`).
func renderTOCChildren(node *tocNode) string {
	var sb strings.Builder
	for _, child := range node.children {
		h := child.heading
		sb.WriteString(`<li class="toc-li toc-h`)
		sb.WriteString(strconv.Itoa(h.Level))
		sb.WriteString(`">`)
		// h.Text is already rendered to safe HTML by Phase 10b
		// (renderWikidotHeadingInline), so emit it verbatim —
		// don't re-escape with html.EscapeString or inline
		// markup shows up as literal source in the TOC.
		sb.WriteString(fmt.Sprintf(
			`<a href="#%s" class="toc-link toc-link-h%d"><span class="toc-text">%s</span></a>`,
			h.ID, h.Level, h.Text))
		if len(child.children) > 0 {
			sb.WriteString(`<ul>`)
			sb.WriteString(renderTOCChildren(child))
			sb.WriteString(`</ul>`)
		}
		sb.WriteString(`</li>`)
	}
	return sb.String()
}

// renderFootnoteList appends a `<ol class="footnotes">` block at
// the end of the document IF any footnote definitions were
// collected in Phase 0. The list items link back to the body
// references (`<a href="#fnref-N">↩</a>`). Order: numeric
// ascending. A `[[footnoteblock]]` marker in the source
// suppresses this append and records an optional
// `title="..."` override for the section label.
func renderFootnoteSection(defs map[string]string, title string) string {
	if len(defs) == 0 {
		return ""
	}
	keys := make([]int, 0, len(defs))
	for k := range defs {
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		keys = append(keys, n)
	}
	sortInts(keys)
	label := title
	if label == "" {
		label = "脚注"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<section class="footnotes"><h2 class="footnotes-title">%s</h2><ol>`, html.EscapeString(label)))
	for _, n := range keys {
		def := defs[strconv.Itoa(n)]
		sb.WriteString(fmt.Sprintf(`<li id="fn-%d">%s <a class="footnote-backref" href="#fnref-%d" title="回到正文">↩</a></li>`, n, inlineOnly(def), n))
	}
	sb.WriteString(`</ol></section>`)
	return sb.String()
}

// sortInts is a tiny insertion sort — Go's stdlib sort package
// is fine, but pulling it in for a 5-key map is overkill, and
// the wikidot parser package already has no other use of sort.
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j] > v {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// ── Table rendering ─────────────────────────────────────────────────────

// renderWikidotTableRowLine parses one `||…||` line into cells. A cell
// starting with `~` is a header cell. Empty cells (from `|| ||` or
// `||||` merges) are collapsed to a single cell with `colspan`
// proportional to the number of `||` separators consumed.
//
// Inline-only on cell content so <p> doesn't end up inside <td>/<th>.
// Authors wanting block content inside a cell should use the
// [[table]]…[[/table]] block syntax with HTML authored directly.
func mapWikidotDateFormat(format string) string {
	if !strings.Contains(format, "$") {
		return format
	}
	// Order matters: longer tokens first so `$YYYY`
	// doesn't get partially replaced by `$YY`. We
	// also handle `$M-$MM` order — the longer token
	// is processed first.
	replacer := strings.NewReplacer(
		"$YYYY", "2006",
		"$YY", "06",
		"$MM", "01",
		"$M", "1",
		"$DD", "02",
		"$D", "2",
		"$HH", "15",
		"$H", "15",
		"$mm", "04",
		"$m", "4",
		"$ss", "05",
		"$s", "5",
	)
	return replacer.Replace(format)
}
