package parsers

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// ── Server-side HTML sanitiser policy ────────────────────────────────
//
// articlePolicy is a bluemonday policy that mirrors — and in some places
// exceeds — what DOMPurify was doing on the client. It runs ONCE per
// render at the server and replaces client-side sanitisation entirely,
// eliminating the ~50 KB gzip DOMPurify dynamic import and the
// multi-megabyte HTML tree walk on the reader's main thread.
//
// All article types (typst, markdown, wikidot, bbcode, html) flow through
// this policy after their type-specific markup has been converted to
// HTML. The policy is intentionally permissive because:
//   - Article content is authored by trusted admins (not end-users).
//   - End-user content (comments, line comments) uses RenderSafeMarkdown
//     which escapes raw HTML and never reaches this code path.
//   - The per-type renderers already enforce URL scheme allow-lists
//     (see sanitizeURLForAttr) and CSS value sanitisation
//     (sanitizeCSSValue), so script/data: URIs are stripped before we
//     get here.
var articlePolicy *bluemonday.Policy

func init() {
	p := bluemonday.NewPolicy()

	// ── Tags ───────────────────────────────────────────────────────
	p.AllowElements(
		// Structural / text
		"a", "abbr", "aside", "b", "blockquote", "br", "cite", "code",
		"details", "div", "dl", "dt", "dd", "em", "figcaption", "figure",
		"h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "ins",
		"kbd", "li", "mark", "ol", "p", "pre", "s", "section", "small",
		"span", "strong", "sub", "summary", "sup", "table", "tbody",
		"td", "th", "thead", "tr", "u", "ul",
		// Media elements (we create <iframe> programatically AFTER
		// sanitise, so iframe does not need to be in the allow-list —
		// in fact it's better if it's stripped from raw input so only
		// our YouTube iframes can appear).
		"video", "audio", "source",
		// SVG (Typst math/diagrams and any embedded SVG)
		"svg", "path", "g", "text", "rect", "circle", "ellipse", "line",
		"polyline", "polygon", "defs", "clippath", "marker", "mask",
		"pattern", "lineargradient", "radialgradient", "stop", "use",
		"image", "foreignobject",
		// MathML (Typst native math, KaTeX server-rendered math)
		"math", "mrow", "mi", "mn", "mo", "msup", "msub", "msubsup",
		"mfrac", "mtable", "mtr", "mtd", "mover", "munder", "munderover",
		"mtext", "mspace", "msqrt", "mroot", "mfenced", "mstack", "mlongdiv",
	)

	// ── Global attributes ──────────────────────────────────────────
	// These are allowed on every element; SVG- and MathML-specific
	// attributes that don't make sense on arbitrary tags are scoped
	// below to reduce XSS surface.
	p.AllowAttrs(
		"href", "src", "title", "alt", "class", "style", "id",
		"name", "colspan", "rowspan", "align", "valign",
		"width", "height", "loading", "decoding",
		"target", "rel", "referrerpolicy", "allowfullscreen", "frameborder",
		"controls", "muted", "loop", "autoplay", "preload", "poster",
		"data-line", "data-tab-id", "data-toggle", "data-source",
		"data-youtube-id",
		"data-math-rendered", "data-math-error",
		"data-mermaid-rendered", "data-mermaid-error",
		"data-typst",
		"aria-hidden", "aria-label", "role",
		"open",
		"xmlns", "xmlns:xlink", "viewbox",
		"preserveaspectratio",
	).Globally()

	// ── SVG-specific attributes ───────────────────────────────────
	// These only make sense on SVG elements; allow them everywhere
	// SVG tags appear (the list matches the SVG element allow-list).
	p.AllowAttrs(
		"d", "fill", "stroke", "stroke-width",
		"x", "y", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry",
		"transform", "font-family", "font-size", "font-weight", "font-style",
		"text-anchor", "dominant-baseline", "points",
		"clip-path", "mask", "fill-opacity", "stroke-opacity",
		"stroke-linecap", "stroke-linejoin", "stroke-dasharray",
		"offset", "stop-color", "stop-opacity",
		"xlink:href",
	).OnElements(
		"svg", "path", "g", "text", "rect", "circle", "ellipse", "line",
		"polyline", "polygon", "defs", "clippath", "marker", "mask",
		"pattern", "lineargradient", "radialgradient", "stop", "use",
		"image", "foreignobject",
	)

	// ── MathML-specific attributes ────────────────────────────────
	p.AllowAttrs(
		"display", "accent", "accentunder", "mathvariant", "mathsize",
		"mathcolor", "mathbackground", "scriptlevel", "displaystyle",
		"form", "fence", "separator", "lspace", "rspace", "stretchy",
		"largeop", "movablelimits", "symmetric", "maxsize", "minsize",
	).OnElements(
		"math", "mrow", "mi", "mn", "mo", "msup", "msub", "msubsup",
		"mfrac", "mtable", "mtr", "mtd", "mover", "munder", "munderover",
		"mtext", "mspace", "msqrt", "mroot", "mfenced",
	)

	// ── URL scheme allow-list ──────────────────────────────────────
	// Only http/https/mailto/tel are permitted as explicit schemes;
	// relative URLs (starting with / or #) are allowed by default in
	// bluemonday for href/src. The YouTube iframe src is set by our
	// postprocess AFTER sanitisation and thus bypasses scheme checking.
	p.AllowURLSchemes("http", "https", "mailto", "tel")

	// ── Hard denies ───────────────────────────────────────────────
	// Tags not in AllowElements (script, object, embed, form, iframe,
	// base, link, meta, etc.) are automatically stripped by bluemonday.
	// All on* event-handler attributes are also stripped by default.

	// Defence-in-depth: require noopener/noreferrer on target=_blank
	// links (our postprocess also adds these explicitly).
	p.RequireNoReferrerOnLinks(true)
	p.RequireNoFollowOnLinks(false)

	articlePolicy = p
}

// youtubeIDRe matches a YouTube video ID (11 chars of [A-Za-z0-9_-]) and
// ensures the surrounding attribute value is exactly the ID (no query
// string / extra chars) so we can safely construct an embed URL.
var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// PostProcessArticleHTML is the server-side finishing pass that runs
// after a per-type renderer (markdown, wikidot, bbcode, html, typst) has
// produced its HTML string. It performs all the static DOM work that the
// client was previously doing in ArticleView.useEffect, so readers get a
// fully-prepared HTML blob and the browser only needs to render it +
// bind event listeners:
//
//  1. Sanitise with bluemonday (replaces client DOMPurify) to strip
//     scripts, event handlers, and dangerous URL schemes.
//  2. Convert Wikidot [[youtube ID]] placeholders to real lazy <iframe>s.
//  3. Add loading="lazy" decoding="async" to all <img> tags.
//  4. Add target="_blank" rel="noopener noreferrer" referrerpolicy to
//     external http(s):// links.
//  5. Add controls to <video>/<audio> tags that don't already have them.
//  6. Walk top-level block elements and assign data-line="N" attributes
//     (stable line numbers for line-comment positioning), skipping
//     nested elements and wikidot structural wrappers.
//  7. Wrap everything in <div class="article-content …" data-processed="1">
//     so the client can detect a pre-processed article and skip its
//     entire mutation pass.
//
// extraClasses are appended to the wrapper class (e.g. "typst-content"
// for typst articles, so type-specific CSS like MathML alignment rules
// continue to apply).
func PostProcessArticleHTML(raw string, extraClasses ...string) string {
	wrapperClass := "article-content"
	for _, c := range extraClasses {
		if c = strings.TrimSpace(c); c != "" {
			wrapperClass += " " + c
		}
	}
	wrapperOpen := `<div class="` + wrapperClass + `" data-processed="1">`
	if strings.TrimSpace(raw) == "" {
		return wrapperOpen + `</div>`
	}

	// Step 1: Sanitise. This must happen BEFORE DOM mutation so that
	// any malicious markup introduced into raw HTML is removed first.
	sanitized := articlePolicy.Sanitize(raw)

	// Step 2-6: parse and transform.
	doc, err := html.Parse(strings.NewReader("<html><body>" + sanitized + "</body></html>"))
	if err != nil {
		// Parser failure on sanitised HTML should be impossible; fall
		// back to returning the sanitised string wrapped in our marker.
		return `<div class="article-content" data-processed="1">` + sanitized + `</div>`
	}

	var bodyNode *html.Node
	var findBody func(*html.Node)
	findBody = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			bodyNode = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBody(c)
		}
	}
	findBody(doc)
	if bodyNode == nil {
		return wrapperOpen + sanitized + `</div>`
	}

	// Detach body children; they'll be re-parented under our wrapper.
	var contentChildren []*html.Node
	for c := bodyNode.FirstChild; c != nil; {
		next := c.NextSibling
		bodyNode.RemoveChild(c)
		contentChildren = append(contentChildren, c)
		c = next
	}

	// Wrapper div.
	wrapper := &html.Node{
		Type: html.ElementNode,
		Data: "div",
		Attr: []html.Attribute{
			{Key: "class", Val: wrapperClass},
			{Key: "data-processed", Val: "1"},
		},
	}

	// Block tags eligible for data-line numbering. We only number
	// top-level block elements (direct children of the body/root flow)
	// that represent "display" content; purely inline wrappers (<span>,
	// <a>, <strong> etc.) are skipped. The list mirrors what the client
	// used to assign line numbers to, minus the catch-all "div" which
	// caused double-numbering of wikidot layout divs.
	isBlock := func(tag string) bool {
		switch tag {
		case "h1", "h2", "h3", "h4", "h5", "h6",
			"p", "pre", "blockquote", "ul", "ol", "table",
			"details", "figure", "section", "hr":
			return true
		}
		return false
	}

	// Helpers to skip children of these containers during numbering
	// (they are structural, not content).
	skipContainerClass := func(n *html.Node) bool {
		cls := getAttr(n, "class")
		if cls == "" {
			return false
		}
		// Wikidot tab panels/navs, collapsible content, aligned divs,
		// TOC — these are layout wrappers, not displayable content lines.
		for _, s := range []string{
			"wikidot-tab-nav", "wikidot-tab-panels", "wikidot-tab-panel",
			"collapsible-content", "wikidot-align", "wikidot-toc",
			"wikidot-youtube", "bbcode-table-wrapper",
		} {
			if strings.Contains(" "+cls+" ", " "+s+" ") {
				return true
			}
		}
		return false
	}

	line := 0
	var childrenToReplace = map[*html.Node]*html.Node{}
	for _, child := range contentChildren {
		// Collect YouTube placeholder replacement (we'll do it after
		// the loop to avoid mutating contentChildren during iteration).
		if child.Type == html.ElementNode && child.Data == "div" {
			if id := getAttr(child, "data-youtube-id"); id != "" && youtubeIDRe.MatchString(id) {
				iframe := &html.Node{
					Type: html.ElementNode,
					Data: "iframe",
					Attr: []html.Attribute{
						{Key: "src", Val: "https://www.youtube.com/embed/" + id},
						{Key: "loading", Val: "lazy"},
						{Key: "allowfullscreen", Val: "allowfullscreen"},
						{Key: "frameborder", Val: "0"},
						{Key: "referrerpolicy", Val: "strict-origin-when-cross-origin"},
						{Key: "title", Val: "YouTube video"},
						{Key: "class", Val: "wikidot-yt-embed"},
					},
				}
				childrenToReplace[child] = iframe
				continue
			}
		}

		// Assign data-line to direct block children, skipping layout
		// containers.
		if child.Type == html.ElementNode && isBlock(child.Data) && !skipContainerClass(child) {
			line++
			setAttr(child, "data-line", itoa(line))
		}

		// Walk subtree for image/link/video enhancements.
		walkHTML(child, func(n *html.Node) {
			if n.Type != html.ElementNode {
				return
			}
			switch n.Data {
			case "img":
				ensureAttr(n, "loading", "lazy")
				ensureAttr(n, "decoding", "async")
			case "a":
				href := getAttr(n, "href")
				if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
					ensureAttr(n, "target", "_blank")
					setAttr(n, "rel", "noopener noreferrer")
					ensureAttr(n, "referrerpolicy", "no-referrer")
				}
			case "video", "audio":
				ensureAttr(n, "controls", "controls")
				ensureAttr(n, "preload", "metadata")
			}
		})
	}

	// Append all children (with placeholders replaced by iframes) to wrapper.
	for _, child := range contentChildren {
		if repl, ok := childrenToReplace[child]; ok {
			wrapper.AppendChild(repl)
		} else {
			wrapper.AppendChild(child)
		}
	}

	bodyNode.AppendChild(wrapper)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return wrapperOpen + sanitized + `</div>`
	}

	s := buf.String()
	i := strings.Index(s, wrapperOpen)
	if i < 0 {
		return wrapperOpen + sanitized + `</div>`
	}
	const close = `</div>`
	j := strings.LastIndex(s, close)
	if j < i {
		return wrapperOpen + sanitized + `</div>`
	}
	return s[i : j+len(close)]
}

// walkHTML visits n and all descendants in depth-first order.
func walkHTML(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(c, fn)
	}
}

// getAttr returns the value of the named attribute, or "".
func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// setAttr sets (adding or replacing) the named attribute.
func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// ensureAttr sets the attribute only if not already present.
func ensureAttr(n *html.Node, key, val string) {
	for _, a := range n.Attr {
		if a.Key == key {
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// itoa converts small non-negative int to string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
