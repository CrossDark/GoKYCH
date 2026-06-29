package parsers

import (
	"fmt"
	"testing"
)

// TestProbeStage4 prints the rendered output for each
// Stage 4 (P2 Stage 2) input. Probe — always passes.
func TestProbeStage4(t *testing.T) {
	cases := []struct {
		label string
		in    string
	}{
		{"Bgcolor_name", "正常 [[bgcolor yellow]]高亮文字[[/bgcolor]] 续"},
		{"Bgcolor_hex", "正常 [[bgcolor #ffeebb]]hex 高亮[[/bgcolor]] 续"},
		{"Bgcolor_dangerous", "[[bgcolor red;expression(alert(1))]]bad[[/bgcolor]]"},
		{"Font", "正常 [[font Courier]]code style[[/font]] 续"},
		{"Font_dangerous", "[[font x; url(javascript:alert(1))]]bad[[/font]]"},
		{"Indent", "[[indent]]\n整段缩进\n跨多行\n[[/indent]]"},
		{"Indent_nested", "[[indent]]外\n[[indent]]内[[/indent]]\n[[/indent]]"},
		{"Iframe", "[[iframe https://www.youtube.com/embed/dQw4w9WgXcQ 560 315]]"},
		{"Iframe_no_size", "[[iframe https://example.com/embed]]"},
		{"Iframe_dangerous", "[[iframe javascript:alert(1)]]"},
		{"Video", "[[video https://example.com/clip.mp4 640 360]]"},
		{"Video_no_size", "[[video https://example.com/clip.mp4]]"},
		{"Video_dangerous", "[[video data:text/html,foo]]"},
		{"Audio", "[[audio https://example.com/song.mp3]]"},
		{"Audio_dangerous", "[[audio javascript:alert(1)]]"},
		{"Date_default", "今天 [[date]]"},
		{"Date_format", "现在 [[date 15:04:05]]"},
		{"Date_wikidot_format", "wikidot 风格 [[date $YYYY-$MM-$DD]]"},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		fmt.Printf("\n=== %s ===\nIN : %q\nOUT: %s\n", c.label, c.in, out)
	}
}
