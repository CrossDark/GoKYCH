package typst

import (
	"strings"
	"testing"
)

func TestPostprocessTypedHTML(t *testing.T) {
	input := `<h2>Hello</h2><p>Hello, world! <math><msup><mi>x</mi></msup></math></p><p>Second paragraph with <a href="https://example.com">link</a> and <img src="/img.png">.</p><pre>code block</pre><ul><li>a</li><li>b</li></ul>`
	out := postprocessTypedHTML(input)

	// Wrapper marker present
	if !strings.Contains(out, `class="typst-content"`) || !strings.Contains(out, `data-typst="1"`) {
		t.Fatal("missing wrapper marker; output:\n", out)
	}

	// Block direct children get data-line sequentially
	if !strings.Contains(out, `<h2 data-line="1">`) {
		t.Error("h2 should be data-line=1, got:", out)
	}
	if !strings.Contains(out, `<p data-line="2">`) {
		t.Error("first <p> should be data-line=2")
	}
	// Third block is another <p> — should be data-line=3
	if !strings.Contains(out, `<p data-line="3">`) {
		t.Error("second <p> should be data-line=3")
	}
	if !strings.Contains(out, `<pre data-line="4">`) {
		t.Error("<pre> should be data-line=4")
	}
	if !strings.Contains(out, `<ul data-line="5">`) {
		t.Error("<ul> should be data-line=5")
	}

	// Nested <li> must NOT get data-line
	if strings.Contains(out, `<li data-line`) {
		t.Error("nested <li> should not have data-line, got:", out)
	}
	// <mi> (math) must NOT get data-line
	if strings.Contains(out, `<mi data-line`) {
		t.Error("math elements should not have data-line")
	}

	// Images get lazy+async
	if !strings.Contains(out, `loading="lazy"`) || !strings.Contains(out, `decoding="async"`) {
		t.Error("images should have loading=lazy and decoding=async")
	}

	// External links get target/rel/referrerpolicy
	if !strings.Contains(out, `target="_blank"`) {
		t.Error("external links should have target=_blank")
	}
	if !strings.Contains(out, `rel="noopener noreferrer"`) {
		t.Error("external links should have noopener noreferrer")
	}
	if !strings.Contains(out, `referrerpolicy="no-referrer"`) {
		t.Error("external links should have referrerpolicy")
	}

	// Wrapper is properly balanced (wrapper opens, content, one closing div)
	if !strings.HasPrefix(out, `<div class="typst-content" data-typst="1">`) {
		t.Error("output should start with wrapper div, got:", out[:200])
	}
	if !strings.HasSuffix(out, `</div>`) {
		t.Error("output should end with closing wrapper div")
	}
}

func TestPostprocessTypedHTMLEmpty(t *testing.T) {
	// Empty body should still produce a valid wrapper.
	out := postprocessTypedHTML("")
	if !strings.Contains(out, `class="typst-content"`) {
		t.Error("empty input should still produce wrapper")
	}
}

func TestPostprocessTypedHTMLPreservesExistingAttrs(t *testing.T) {
	// If an image already has loading="eager", don't overwrite it.
	input := `<p><img src="hero.png" loading="eager"></p>`
	out := postprocessTypedHTML(input)
	if strings.Contains(out, `loading="lazy"`) {
		t.Error("should not overwrite existing loading=eager, got:", out)
	}
	// decoding="async" should still be added.
	if !strings.Contains(out, `decoding="async"`) {
		t.Error("should add decoding=async to images without it")
	}
}
