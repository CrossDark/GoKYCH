package parsers

import "testing"

// These are regression tests for the XSS guards in bbcode.go / wikidot.go.
// Each payload represents a real attack that survives the raw
// `html.EscapeString` pass but must be dropped by the sanitiser.

// ── sanitizeURLForAttr ────────────────────────────────────────────────

func TestSanitizeURLAllowsSafeURLs(t *testing.T) {
	allowed := []string{
		"/local/path",
		"/labels/foo",
		"http://example.com",
		"https://example.com/path?q=1",
		"mailto:user@example.com",
		"  https://example.com  ", // whitespace tolerated
	}
	for _, u := range allowed {
		if got := sanitizeURLForAttr(u); got == "" {
			t.Errorf("expected %q to be allowed, got empty", u)
		}
	}
}

func TestSanitizeURLRejectsDangerousSchemes(t *testing.T) {
	rejected := []string{
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)", // case-insensitive scheme match
		"vbscript:msgbox",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"  javascript:alert(1)", // leading whitespace shouldn't bypass
		"Java\nscript:alert(1)", // embedded newline / obfuscation
	}
	for _, u := range rejected {
		if got := sanitizeURLForAttr(u); got != "" {
			t.Errorf("expected %q to be rejected, got %q", u, got)
		}
	}
}

func TestSanitizeURLRejectsProtocolRelativeAndBackslash(t *testing.T) {
	// Browsers normalise "//evil.com" into a host change and "/\evil.com"
	// into a similar escape; both must be rejected even though they start
	// with a slash.
	rejected := []string{
		"//evil.com",
		"//evil.com/path",
		"/\\evil.com",
		"/\\\\evil.com",
	}
	for _, u := range rejected {
		if got := sanitizeURLForAttr(u); got != "" {
			t.Errorf("expected %q to be rejected, got %q", u, got)
		}
	}
}

func TestSanitizeURLRejectsAttributeInjection(t *testing.T) {
	// `[[[" onmouseover=...]]]`-style payload — Wikidot appends a /wikidot/
	// prefix then this gets concatenated into `<a href="%s">`. The single
	// quote must close the href attribute cleanly, so the result must NOT
	// pass through.
	if got := sanitizeURLForAttr(`/wikidot/" onmouseover="alert(1)`); got != "" {
		t.Errorf("attribute-injection URL should be rejected, got %q", got)
	}
}

// ── sanitizeCSSValue ─────────────────────────────────────────────────

func TestSanitizeCSSAllowsSafeValues(t *testing.T) {
	allowed := []string{
		"16px",
		"1.25em",
		"#fff",
		"#3b82f6",
		"red",
		"system-ui",
		"Inter, sans-serif",
		"100%",
		"12pt",
	}
	for _, v := range allowed {
		if got := sanitizeCSSValue(v); got == "" {
			t.Errorf("expected %q to be allowed, got empty", v)
		}
	}
}

func TestSanitizeCSSRejectsMetacharacters(t *testing.T) {
	rejected := []string{
		"red; background: url(javascript:alert(1))", // semicolon + url()
		"red} body { display:none",                  // closing brace
		"red { color:red }",                         // opening brace
		"expression(alert(1))",                      // IE CSS expression
		"url(javascript:alert(1))",                  // url() in any form
		"JAVASCRIPT:alert(1)",                       // obfuscated
		"@import url(http://evil)",                  // @import injection
		"red)foo(bar",                               // parentheses
	}
	for _, v := range rejected {
		if got := sanitizeCSSValue(v); got != "" {
			t.Errorf("expected %q to be rejected, got %q", v, got)
		}
	}
}

func TestSanitizeCSSRejectsAttributeEscapes(t *testing.T) {
	// Even if the rest is benign-looking, a quote or angle bracket must
	// close the value — drop the whole thing.
	rejected := []string{
		`red" onclick="alert(1)`, // double-quote attribute break
		"red'onclick='alert(1)",  // single-quote attribute break
		"red<>",                  // angle bracket
		"red\\22",                // backslash-escaped quote
	}
	for _, v := range rejected {
		if got := sanitizeCSSValue(v); got != "" {
			t.Errorf("expected %q to be rejected, got %q", v, got)
		}
	}
}

// ── sanitizeAnchorID ─────────────────────────────────────────────────

func TestSanitizeAnchorIDAllowsSafeIDs(t *testing.T) {
	allowed := []string{
		"section-1",
		"my_section",
		"abc123",
		"_private",
		"A",
	}
	for _, id := range allowed {
		if got := sanitizeAnchorID(id); got == "" {
			t.Errorf("expected %q to be allowed, got empty", id)
		}
	}
}

func TestSanitizeAnchorIDRejectsInjection(t *testing.T) {
	rejected := []string{
		`section" onclick="evil"`, // quote injection
		"section<",                // angle bracket
		"section space",           // space
		"section\nfoo",            // newline
		"section.foo",             // punctuation
	}
	for _, id := range rejected {
		if got := sanitizeAnchorID(id); got != "" {
			t.Errorf("expected %q to be rejected, got %q", id, got)
		}
	}
}

// ── End-to-end parser tests ─────────────────────────────────────────

func TestRenderBBCodeStripsJavaScriptURL(t *testing.T) {
	// The exact payload from the audit — must not produce a live href.
	in := `[url=javascript:alert(1)]click[/url]`
	out := RenderBBCode(in)
	if contains(out, `javascript:`) {
		t.Fatalf("BBCode allowed javascript: URL through: %q", out)
	}
}

func TestRenderBBCodeStripsDataImageURL(t *testing.T) {
	in := `[img]data:text/html,<script>alert(1)</script>[/img]`
	out := RenderBBCode(in)
	if contains(out, `data:text/html`) {
		t.Fatalf("BBCode allowed data: URL through: %q", out)
	}
}

func TestRenderBBCodeStripsCSSInjection(t *testing.T) {
	in := `[size=12px; background:url(javascript:alert(1))]x[/size]`
	out := RenderBBCode(in)
	// The whole span must either be absent (sanitiser dropped the value) or
	// contain only the inner text "x" — never with a live style payload.
	if contains(out, `javascript:`) || contains(out, `; background:`) {
		t.Fatalf("BBCode leaked style payload: %q", out)
	}
}

func TestRenderWikidotStripsWikilinkInjection(t *testing.T) {
	// `[[[" onmouseover=alert(1) x="]]]` was a confirmed XSS path. The output
	// must not contain a live `onmouseover="..."` attribute — escaped text
	// (e.g. `onmouseover=&#34;...&#34;`) is fine because the browser renders
	// it as plain text inside the paragraph, not as an attribute.
	in := `[[[" onmouseover="alert(1)" x="]]]`
	out := RenderWikidot(in)
	if contains(out, `onmouseover="`) {
		t.Fatalf("Wikidot leaked attribute injection: %q", out)
	}
	// Belt-and-braces: the output also shouldn't contain an unescaped quote
	// adjacent to "onmouseover" (which would indicate partial escaping that
	// a future renderer might re-interpret).
	if contains(out, `onmouseover="alert(1)`) {
		t.Fatalf("Wikidot leaked live JS: %q", out)
	}
}

func TestRenderWikidotStripsImageScheme(t *testing.T) {
	in := `[[image javascript:alert(1) ]]`
	out := RenderWikidot(in)
	if contains(out, `javascript:`) {
		t.Fatalf("Wikidot [[image]] allowed javascript: URL through: %q", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
