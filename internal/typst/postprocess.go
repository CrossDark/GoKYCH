package typst

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// postprocessTypedHTML transforms Typst's raw HTML body into the final
// cached form that gets served to readers. All transformations here are
// STATIC: they depend only on the HTML markup, not on the viewer or
// request context, so they can be done ONCE at compile time and then
// served as a blob from the DB cache. This eliminates the client-side
// DOM walk entirely for Typst articles — the client only has to bind
// event listeners and measure comment bubble positions.
//
// Transformations applied:
//  1. Wraps content in <div class="typst-content" data-typst="1"> marker.
//  2. Adds data-line="N" to direct-child block elements (matches the
//     frontend selector used for line-comment positioning: h1-h6, p,
//     pre, blockquote, ul, ol, table). This way the server assigns
//     stable line numbers at compile time and the client never has to
//     run a querySelectorAll loop over the entire article.
//  3. Adds loading="lazy" decoding="async" to all <img> tags so images
//     below the fold don't block the initial render.
//  4. Adds target="_blank" rel="noopener noreferrer" to external links
//     (those whose href starts with http:// or https://), plus
//     referrerpolicy="no-referrer" for privacy. Relative links and
//     anchor links (#, /) are left alone.
//
// All transformations are safe to apply to typst-compiler output: typst
// generates well-formed HTML5 with no scripts or user-injected markup,
// and x/net/html handles SVG + MathML foreign content per the HTML5
// parsing spec.
func postprocessTypedHTML(body string) string {
	// html.Parse expects a document; wrap in <html><body> so the parser
	// places our content fragments in the right place. We then extract
	// the body's children, wrap them in our marker div, and re-serialize.
	doc, err := html.Parse(strings.NewReader("<html><body>" + body + "</body></html>"))
	if err != nil {
		// Fallback: parser failure should be impossible for typst output
		// (it's well-formed), but be defensive and wrap the raw body.
		return `<div class="typst-content" data-typst="1">` + body + `</div>`
	}

	// Find <body> element.
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
		return `<div class="typst-content" data-typst="1">` + body + `</div>`
	}

	// Collect children of body (these are the top-level content nodes
	// typst emitted). We detach them from the parsed <body> because
	// they'll become children of our wrapper div.
	var contentChildren []*html.Node
	for c := bodyNode.FirstChild; c != nil; {
		next := c.NextSibling
		bodyNode.RemoveChild(c)
		contentChildren = append(contentChildren, c)
		c = next
	}

	// Create the wrapper div with class + marker attribute.
	wrapper := &html.Node{
		Type: html.ElementNode,
		Data: "div",
		Attr: []html.Attribute{
			{Key: "class", Val: "typst-content"},
			{Key: "data-typst", Val: "1"},
		},
	}

	// Block tags that receive data-line numbering (must match the
	// selector the frontend used to use for typst line numbering).
	isBlock := func(tag string) bool {
		switch tag {
		case "h1", "h2", "h3", "h4", "h5", "h6",
			"p", "pre", "blockquote", "ul", "ol", "table":
			return true
		}
		return false
	}

	line := 0
	for _, child := range contentChildren {
		wrapper.AppendChild(child)
		// Only number element nodes whose tag is a recognized block
		// element. Text nodes (whitespace between elements), comments,
		// and non-block wrappers don't count.
		if child.Type == html.ElementNode && isBlock(child.Data) {
			line++
			setAttr(child, "data-line", itoa(line))
		}

		// Walk subtree for <img> tag enhancements and external link
		// attributes. data-line on non-top-level elements isn't added
		// (matches the frontend's direct-child selector).
		walk(child, func(n *html.Node) {
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
					setAttr(n, "target", "_blank")
					setAttr(n, "rel", "noopener noreferrer")
					ensureAttr(n, "referrerpolicy", "no-referrer")
				}
			}
		})
	}

	// Re-attach the wrapper to body so html.Render emits it correctly.
	bodyNode.AppendChild(wrapper)

	var buf bytes.Buffer
	// Render only the wrapper's inner serialization — we don't want the
	// <html><head><body> scaffolding that html.Parse created. Easiest
	// approach: render the whole document then extract the wrapper div.
	if err := html.Render(&buf, doc); err != nil {
		return `<div class="typst-content" data-typst="1">` + body + `</div>`
	}

	// Extract the portion between our wrapper open and close tags.
	// Using string search is simpler than serialising just the subtree
	// (which html.Render can do but requires handling the doctype/head
	// correctly).
	s := buf.String()
	const open = `<div class="typst-content" data-typst="1">`
	const close = `</div>`
	i := strings.Index(s, open)
	if i < 0 {
		return `<div class="typst-content" data-typst="1">` + body + `</div>`
	}
	// Find last </div> before </body></html> — that closes our wrapper.
	j := strings.LastIndex(s, close)
	if j < i {
		return `<div class="typst-content" data-typst="1">` + body + `</div>`
	}
	return s[i : j+len(close)]
}

// walk visits n and all descendants in depth-first order, calling fn for
// each node.
func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
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

// ensureAttr sets the attribute only if it is not already present, so
// values authored in the source (e.g. loading="eager" on a hero image)
// are preserved.
func ensureAttr(n *html.Node, key, val string) {
	for _, a := range n.Attr {
		if a.Key == key {
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// itoa cheaply converts a small int to string (avoid strconv import for
// a single caller).
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
