package parsers

import (
	"html"
	"sort"
	"strings"
)

func renderWikidotAdvancedLists(text string) string {
	// Tokenise: collect every (position, kind, attrs) entry
	// in source order. A "kind" is one of "ul", "ol", "li",
	// "/li", "/ul", "/ol". The walker then visits tokens
	// left-to-right.
	toks := []advListTok{}
	for _, m := range reWDUL.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "ul", text[m[0]:m[1]], text[m[2]:m[3]]})
	}
	for _, m := range reWDOL.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "ol", text[m[0]:m[1]], text[m[2]:m[3]]})
	}
	for _, m := range reWDLIOpen.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "li", text[m[0]:m[1]], text[m[2]:m[3]]})
	}
	for _, m := range reWDLIClose.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "/li", text[m[0]:m[1]], ""})
	}
	for _, m := range reWDULClose.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "/ul", text[m[0]:m[1]], ""})
	}
	for _, m := range reWDOLClose.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "/ol", text[m[0]:m[1]], ""})
	}
	// Sort by position (stable order is needed because
	// FindAllSubmatchIndex returns results in
	// source-order, but mixing multiple FindAll calls
	// across the same source keeps that order as long
	// as the source is unchanged).
	sortToks(toks)
	if len(toks) == 0 {
		return text
	}

	// Walk tokens. Each `[[ul]]` / `[[ol]]` opens a new
	// list level; each `[[li]]` opens an item; `[[/li]]`,
	// `[[/ul]]`, `[[/ol]]` close. We collect rendered
	// spans per top-level match range (start, end), then
	// replace those ranges in the source.
	type listFrame struct {
		tag   string // "ul" or "ol"
		attrs string
		open  bool // has the `<tag>` been emitted?
	}
	type itemFrame struct {
		attrs   string
		open    bool
		hasBody bool // at least one body segment written
	}
	var sb strings.Builder
	last := 0
	// Stack of frames. The top of the stack is the
	// innermost open list; under it is the parent list
	// (or the parent item). Each frame also tracks
	// whether the parent is a list or an item.
	type frameKind int
	const (
		frameList frameKind = iota
		frameItem
	)
	type frame struct {
		kind  frameKind
		tag   string // "ul" / "ol" for lists, "" for items
		attrs string
	}
	var stack []frame
	flushItem := func() {
		// Close the innermost open item, if any.
		// An item is "open" if its frame is on
		// the stack — the stack itself tracks
		// open/close state, so we just need to
		// check the top-of-stack kind. (Earlier
		// versions used `attrs != ""` as a
		// proxy for "open" which broke bare
		// `[[li]]` items because they have no
		// attributes.)
		if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
			sb.WriteString("</li>")
		}
	}
	flushList := func() {
		// Close one list level.
		if len(stack) > 0 && stack[len(stack)-1].kind == frameList {
			sb.WriteString("</")
			sb.WriteString(stack[len(stack)-1].tag)
			sb.WriteString(">")
		}
	}
	for _, t := range toks {
		// Text between the previous token and this one
		// is body content. We append it to the
		// innermost item (if any) as inlineOnly'd
		// HTML.
		if t.pos > last {
			body := text[last:t.pos]
			if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
				sb.WriteString(inlineOnly(body))
			} else {
				// Body outside any list — pass
				// through (the caller wraps it).
				sb.WriteString(body)
			}
		}
		switch t.kind {
		case "ul", "ol":
			// A new list opens NESTED inside any
			// currently-open item (HTML allows
			// <ul> as a child of <li>), so we
			// do NOT flush the current item
			// here. We only flush a previous
			// list of a DIFFERENT marker (mixing
			// `[[ul]]` and `[[ol]]` at the same
			// level closes the previous one and
			// opens a new one) — that's the only
			// case where a list-open also ends a
			// list.
			if len(stack) > 0 && stack[len(stack)-1].kind == frameList && stack[len(stack)-1].tag != t.kind {
				// Close any open item in the
				// previous list before closing
				// the list itself.
				flushItem()
				flushList()
				stack = stack[:len(stack)-1]
			}
			attrs := parseCollapsibleAttrs(t.attrs)
			style := styleFromAttrs(attrs)
			cls := classFromAttrs(attrs)
			extra := ""
			if cls != "" {
				extra += " class=\"" + cls + "\""
			}
			if style != "" {
				extra += " style=\"" + style + "\""
			}
			extra += extraAttrsFromAttrs(attrs)
			sb.WriteString("<")
			sb.WriteString(t.kind)
			sb.WriteString(">")
			if extra != "" {
				// Inject into the open tag — rewind
				// the just-written `<tag>` and
				// re-emit with attributes. We keep
				// the leading `<` so the rewound
				// length is `len(t.kind)+1` (the
				// tag chars + the closing `>`),
				// not `len(t.kind)+2` (which
				// would eat the `<` too).
				written := sb.String()
				sb.Reset()
				sb.WriteString(written[:len(written)-len(t.kind)-1])
				sb.WriteString(t.kind)
				sb.WriteString(extra)
				sb.WriteString(">")
			}
			stack = append(stack, frame{kind: frameList, tag: t.kind, attrs: t.attrs})
		case "li":
			// Close any currently-open item first.
			flushItem()
			if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
				// Nested without an item close —
				// ignore (the author wrote
				// `[[li]][[li]]` which is invalid).
				continue
			}
			if len(stack) == 0 || stack[len(stack)-1].kind == frameList {
				// Implicit list start: emit a
				// `<ul>` so the `<li>` is
				// well-formed.
				if len(stack) == 0 {
					sb.WriteString("<ul>")
					stack = append(stack, frame{kind: frameList, tag: "ul", attrs: ""})
				}
			}
			attrs := parseCollapsibleAttrs(t.attrs)
			style := styleFromAttrs(attrs)
			cls := classFromAttrs(attrs)
			extra := ""
			if cls != "" {
				extra += " class=\"" + cls + "\""
			}
			if style != "" {
				extra += " style=\"" + style + "\""
			}
			extra += extraAttrsFromAttrs(attrs)
			sb.WriteString("<li")
			sb.WriteString(extra)
			sb.WriteString(">")
			stack = append(stack, frame{kind: frameItem, attrs: t.attrs})
		case "/li":
			flushItem()
			if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
				stack = stack[:len(stack)-1]
			}
		case "/ul", "/ol":
			// Close any open item first.
			flushItem()
			// Pop frames until we find a matching list.
			tag := t.kind[1:] // strip leading "/"
			popped := false
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].kind == frameList && stack[i].tag == tag {
					// Pop everything up to and
					// including this list.
					for j := len(stack) - 1; j >= i; j-- {
						if stack[j].kind == frameItem {
							sb.WriteString("</li>")
						} else {
							sb.WriteString("</")
							sb.WriteString(stack[j].tag)
							sb.WriteString(">")
						}
					}
					stack = stack[:i]
					popped = true
					break
				}
			}
			if !popped {
				// Unmatched close — ignore.
			}
		}
		last = t.end
	}
	// Trailing text after the last token.
	if last < len(text) {
		body := text[last:]
		if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
			sb.WriteString(inlineOnly(body))
		} else {
			sb.WriteString(body)
		}
	}
	// Close any frames still on the stack.
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == frameItem {
			sb.WriteString("</li>")
		} else {
			sb.WriteString("</")
			sb.WriteString(stack[i].tag)
			sb.WriteString(">")
		}
	}
	// Splice the rendered list block into the source
	// wherever a top-level list was found. We do this
	// by walking the source again and replacing each
	// `[[ul]]` (or `[[ol]]`) with the rendered output.
	// Since we only have one rendered string, we use
	// the simple approach: the advanced-list region is
	// the smallest span covering every advanced-list
	// token, and the rendered output replaces that
	// span verbatim.
	if len(toks) == 0 {
		return text
	}
	first := toks[0].pos
	lastEnd := toks[0].end
	for _, t := range toks[1:] {
		if t.pos > lastEnd {
			// Non-contiguous — emit the
			// currently-built list, then
			// preserve the gap, then start a
			// new list region.
			// (We don't currently handle this
			// case; advanced-list authors
			// always write contiguous blocks.)
		}
		if t.end > lastEnd {
			lastEnd = t.end
		}
	}
	// We always produce exactly ONE rendered block per
	// pass and replace the entire token span. This
	// handles the common case where one article has
	// multiple separate advanced-list blocks only if
	// they don't interleave; if they do, the rendered
	// output is concatenated into one block. We
	// accept that limitation for the spec round 2
	// scope.
	return text[:first] + sb.String() + text[lastEnd:]
}

// sortToks is a tiny insertion sort used by
// renderWikidotAdvancedLists to keep token positions
// in source order (the multiple FindAllStringSubmatchIndex
// scans are returned in source order individually, but
// their union is a merge that needs an explicit sort).
func sortToks(a []advListTok) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j].pos > v.pos {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// styleFromAttrs / classFromAttrs extract the style /
// class keys from a parsed attribute map (used by
// renderWikidotAdvancedLists). We split these out
// because the map-merge logic in renderWikidotAdvancedLists
// builds up attributes from `data-*` keys too, and we
// want to keep the in-tag attribute order stable
// (class, style, data-…) for predictable test output.
func styleFromAttrs(attrs map[string]string) string {
	s, ok := attrs["style"]
	if !ok {
		return ""
	}
	return sanitizeCSSValue(s)
}

func classFromAttrs(attrs map[string]string) string {
	c, ok := attrs["class"]
	if !ok {
		return ""
	}
	return sanitizeAnchorID(c)
}

// extraAttrsFromAttrs serialises every attribute key that
// isn't a special collapsible/list key (`show`, `hide`,
// `folded`, `hideLocation`, `class`, `style`) into a
// stable HTML-attribute tail. This is what surfaces
// `data-toggle="data1"`, `id="…"`, `role="…"`, etc. on
// advanced-list open tags. Keys are HTML-escaped (the
// value was already escaped when the regex captured it,
// but we run EscapeString again as a defence in depth).
func extraAttrsFromAttrs(attrs map[string]string) string {
	skip := map[string]bool{
		"show": true, "hide": true,
		"folded": true, "hideLocation": true,
		"class": true, "style": true,
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		if skip[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output for tests
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(html.EscapeString(attrs[k]))
		sb.WriteString(`"`)
	}
	return sb.String()
}

// slugifyUsername converts a wikidot user-mention name
// to a URL-safe slug. Wikidot usernames can contain
// spaces and any printable character; the user-profile
// route (`/user/<slug>`) should resolve by an
// un-ambiguous, lowercased slug. We allow `[A-Za-z0-9_-]`
// and map everything else to `-`, then collapse runs of
// `-` and trim leading / trailing `-`. The result is
// safe to drop into an `<a href="...">` (no `<`, `>`,
// `"` or `'` survives) without an additional pass.

// mapWikidotDateFormat accepts either a Go time-format
// string or a Wikidot-style format string (the latter
// uses `$YYYY` / `$MM` / `$DD` / `$HH` etc.). Wikidot
// migrated articles tend to use the Wikidot form; we
// translate the documented tokens to Go's layout so
// the renderer doesn't silently produce garbage on
// legacy pages. Anything not matching a documented
// token is returned unchanged (Go's `time.Format` will
// pass through unknown literals).
//
// renderWikidotIndentBlocks scans `source` left-to-right
// for `[[indent]]` open tags, walks past any nested
// `[[indent]]` opens (counting depth), and replaces each
// matched `[[indent]]…[[/indent]]` block with the
// rendered `<div class="wikidot-indent">`. The depth
// counter is what makes nested indents work — a regex
// can't count opens, so a non-greedy `.*?` between the
// open and close tags would consume the FIRST inner
// `[[/indent]]` it sees, breaking the outer block.
//
// Unmatched `[[indent]]` (no close anywhere downstream)
// is left as raw text so the author can see the typo.
func renderWikidotDefList(text string) string {
	// We walk the source line by line, but the
	// rendered `<dl>…</dl>` is emitted as a single
	// contiguous string with NO internal newlines.
	// Continuation lines append `<br />…` to the
	// previous `<dd>` (re-opening the `<dd>` by
	// stripping the previous `</dd>` and writing a
	// new `…<br />…</dd>`). The result is a single
	// line that the wrap phase recognises as a block
	// (via the `dl` token in `blockInBuf`).
	var sb strings.Builder
	inDL := false
	lastWasDD := false
	flushClose := func() {
		if inDL {
			sb.WriteString("</dl>")
			inDL = false
			lastWasDD = false
		}
	}
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		trimmed := strings.TrimRight(raw, "\r")
		// We emit a `\n` BETWEEN source lines so
		// the rest of the wrap pass sees the
		// original line structure. The exception
		// is INSIDE a `<dl>` block: there the
		// def items must stay on a single line
		// (or the wrap phase will treat each
		// `<dt>` line as a paragraph and wrap
		// them in `<p>`). So we suppress the
		// trailing `\n` after a def item or
		// continuation, and re-emit it only
		// when the next non-def line forces the
		// `<dl>` to close.
		emitNewline := func() {
			if i < len(lines)-1 {
				sb.WriteString("\n")
			}
		}
		if m := reWDDefItem.FindStringSubmatch(trimmed); m != nil {
			if !inDL {
				sb.WriteString("<dl>")
				inDL = true
			}
			term := m[1]
			def := m[2]
			sb.WriteString("<dt>")
			sb.WriteString(inlineOnly(term))
			sb.WriteString("</dt><dd>")
			sb.WriteString(inlineOnly(def))
			sb.WriteString("</dd>")
			lastWasDD = true
			// No trailing `\n` — the next item
			// (or the `</dl>` close) is on the
			// same line.
		} else if inDL && lastWasDD {
			cm := reWDDefCont.FindStringSubmatch(trimmed)
			if cm != nil && !strings.Contains(cm[1], ":") {
				written := sb.String()
				if strings.HasSuffix(written, "</dd>") {
					sb.Reset()
					sb.WriteString(written[:len(written)-len("</dd>")])
				}
				sb.WriteString("<br />")
				sb.WriteString(inlineOnly(cm[1]))
				sb.WriteString("</dd>")
				// No trailing `\n`.
			} else {
				// Non-def line in the middle
				// of a `<dl>` — close the
				// block, emit the line
				// verbatim, and re-emit the
				// `\n` so the surrounding
				// text structure is preserved.
				flushClose()
				sb.WriteString(trimmed)
				emitNewline()
			}
		} else {
			sb.WriteString(trimmed)
			emitNewline()
		}
	}
	flushClose()
	return sb.String()
}

func renderWikidotLists(text string) string {
	// Pre-pass: convert advanced `[[ul]]` / `[[ol]]` /
	// `[[li]]` blocks to a stashed `<ul>/<ol>` block
	// before the line-based `*` / `#` list pass runs.
	// The advanced form is parsed by a stack-based
	// walker (nested `<ul>`/`<ol>` allowed) and the
	// output is a single HTML string that the wrap
	// phase treats as block-level.
	text = renderWikidotAdvancedLists(text)

	// Pre-pass: convert definition-list lines into
	// stashed HTML placeholders. We do this BEFORE the
	// `*` / `#` list pass so a definition list doesn't
	// accidentally trigger the list regex (the leading
	// `:` is not a list marker, but the lines are
	// handled by a separate code path that the list
	// pass can safely ignore). The output is a single
	// `<dl>...</dl>` block that the wrap phase treats
	// as block-level and never wraps in <p>.
	text = renderWikidotDefList(text)

	// Split on newlines but keep blank-line groups together — a
	// blank line breaks the current list (Wikidot doesn't fuse
	// lists across blank lines, and the paragraph wrapper would
	// otherwise wrap the gap in <p>).
	lines := strings.Split(text, "\n")
	var out strings.Builder
	i := 0
	for i < len(lines) {
		j := i
		// Find a contiguous run of list-able lines.
		for j < len(lines) {
			t := strings.TrimLeft(lines[j], " \t")
			if !(strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "# ")) {
				break
			}
			// But a blank line in the middle breaks the run.
			if strings.TrimSpace(lines[j]) == "" {
				break
			}
			j++
		}
		if j == i {
			out.WriteString(lines[i])
			if i < len(lines)-1 {
				out.WriteString("\n")
			}
			i++
			continue
		}
		out.WriteString(renderListBlock(lines[i:j]))
		if j < len(lines) {
			out.WriteString("\n")
		}
		i = j
	}
	return out.String()
}

// renderListBlock turns a slice of `* ` / `# ` lines (all part of
// one list region) into the corresponding nested HTML. We walk the
// lines with an explicit depth stack so the open/close order is
// always well-formed.
//
// Wikidot's nesting rules (mirrored here):
//   - 2 spaces of leading indent = 1 level of nesting depth.
//   - A child list sits INSIDE its parent's <li>: the parent's
//     <li> stays open until the child list is fully closed.
//   - Mixing `*` and `#` at the same level closes the current
//     list and opens a new one of the other type.
//
// Bug history: an earlier version of this function used the
// marker character itself ("*" / "#") as the HTML tag name,
// emitting `<*>` / `<#>` instead of `<ul>` / `<ol>`. That was
// visually plausible (browsers treat unknown tags as inline
// elements and don't break the layout) but rendered as
// content-less anchors and broke the DOM-based line-comment
// targeting. Fixed by routing every list open/close through
// listTagForMarker.
func renderListBlock(lines []string) string {
	type item struct {
		marker string // "*" or "#"
		indent int    // 0 = depth 1, 1 = depth 2, etc.
		body   string
	}
	items := make([]item, 0, len(lines))
	for _, line := range lines {
		leading := len(line) - len(strings.TrimLeft(line, " \t"))
		// Wikidot uses 2 spaces per indent level. Halve, rounding
		// down so a 1-space indent is still depth 1 (more
		// forgiving than the old "depth = leading/2 exactly").
		depth := leading / 2
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "* ") {
			items = append(items, item{"*", depth, t[2:]})
		} else if strings.HasPrefix(t, "# ") {
			items = append(items, item{"#", depth, t[2:]})
		} else {
			items = append(items, item{"", depth, t})
		}
	}

	var sb strings.Builder
	// openStack holds the marker for each currently-open list
	// (top = innermost). len(openStack) == current depth.
	openStack := make([]string, 0, 4)

	// emitLiOpen is called for every item to bring the open
	// <ul>/<ol> nesting to the right level for the upcoming
	// <li>. After this, the matching <li> has its parent list
	// open and any shallower lists closed. The caller still
	// has to decide whether to defer the </li> based on the
	// NEXT item's depth.
	emitLiOpen := func(it item) {
		target := it.indent + 1
		// Close levels that are deeper than we need now.
		for len(openStack) > target {
			sb.WriteString("</")
			sb.WriteString(listTagForMarker(openStack[len(openStack)-1]))
			sb.WriteString(">")
			openStack = openStack[:len(openStack)-1]
		}
		// Open levels until we hit the target depth.
		for len(openStack) < target {
			// Pick the marker: prefer the current item's
			// marker when we're opening fresh (no parent
			// list to inherit from), else fall back to the
			// last open marker so a continued `*` at the
			// same depth nests under the same <ul>.
			var m string
			if len(openStack) == 0 || (len(openStack) < it.indent) {
				if it.marker != "" {
					m = it.marker
				} else {
					m = "*"
				}
			} else {
				m = openStack[len(openStack)-1]
			}
			sb.WriteString("<")
			sb.WriteString(listTagForMarker(m))
			sb.WriteString(">")
			openStack = append(openStack, m)
		}
		// Same depth but marker differs → close & reopen
		// with the right marker (sibling list, not nested).
		if len(openStack) > 0 && it.marker != "" && openStack[len(openStack)-1] != it.marker {
			last := openStack[len(openStack)-1]
			sb.WriteString("</")
			sb.WriteString(listTagForMarker(last))
			sb.WriteString(">")
			openStack = openStack[:len(openStack)-1]
			sb.WriteString("<")
			sb.WriteString(listTagForMarker(it.marker))
			sb.WriteString(">")
			openStack = append(openStack, it.marker)
		}
	}

	for idx, it := range items {
		emitLiOpen(it)
		sb.WriteString("<li>")
		sb.WriteString(inlineOnly(it.body))

		// If the NEXT item is a deeper-nested child, leave
		// the <li> open so the child list nests inside it.
		// Otherwise close the <li> here.
		if idx == len(items)-1 {
			// last item; we'll close all open lists
			// after this iteration. Add a final </li>.
			sb.WriteString("</li>")
		} else {
			next := items[idx+1]
			if next.indent > it.indent {
				// child list coming — keep <li> open
				continue
			}
			sb.WriteString("</li>")
		}
	}
	// Close any remaining open lists.
	for len(openStack) > 0 {
		sb.WriteString("</")
		sb.WriteString(listTagForMarker(openStack[len(openStack)-1]))
		sb.WriteString(">")
		openStack = openStack[:len(openStack)-1]
	}
	return sb.String()
}

// listTagForMarker maps a wikidot list marker (`*` or `#`) to the
// corresponding HTML tag (`ul` or `ol`). A default of `ul` is used
// for the empty marker (defensive — the caller filters these out
// before invoking the open/close helpers, but a stray empty marker
// shouldn't break the output).
func listTagForMarker(m string) string {
	if m == "#" {
		return "ol"
	}
	return "ul"
}

// renderDivBlocks handles `[[div class="..."]]` and
// `[[div style="..."]]` blocks including arbitrary nesting. Regex
// non-greedy matching can't pair nested brackets correctly — the
// first `[[/div]]` always wins, leaving the rest as raw source.
// We use a manual byte-level scan that counts open/close tokens
// from each `[[div` until depth returns to zero.
//
// Recognised open tokens:
//   - `[[div class="..."]]`
//   - `[[div style="..."]]`
//   - `[[div data-<name>="..."]]` (Stage 4 addition —
//     data-* attributes are forwarded
//     verbatim so authors can hook
//     custom JS or framework-specific
//     attributes without escaping through
//     a generic `key="..."` form that
//     would risk CSS injection via the
//     `data-` namespace.)
//
// Close token: `[[/div]]`. Anything that looks like
// `[[div anything-else]]` is left alone (so future [[div class=…
// without quotes, or [[div id=…]], doesn't trip this parser).
