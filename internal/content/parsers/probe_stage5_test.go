package parsers

import (
	"fmt"
	"testing"
)

// TestProbeStage5 prints the rendered output for each
// tabview input. Probe — always passes.
func TestProbeStage5(t *testing.T) {
	cases := []struct {
		label string
		in    string
	}{
		{"Tabview_basic", "[[tabview]]\n[[tab 第一页]]\n内容 1\n[[/tab]]\n[[tab 第二页]]\n内容 2\n[[/tab]]\n[[/tabview]]"},
		{"Tabview_three", "[[tabview]]\n[[tab A]]\n## A ##\n[[/tab]]\n[[tab B]]\n## B ##\n[[/tab]]\n[[tab C]]\n## C ##\n[[/tab]]\n[[/tabview]]"},
		{"Tabview_empty", "[[tabview]]\n[[/tabview]]"},
		{"Tabview_single", "[[tabview]]\n[[tab 唯一]]\n## 唯一内容 ##\n[[/tab]]\n[[/tabview]]"},
		{"Tabview_html_in_body", "[[tabview]]\n[[tab 页1]]\n[[div style=\"color:red\"]]红色[[/div]]\n[[/tab]]\n[[/tabview]]"},
		{"Tabview_unmatched", "[[tabview]]\n[[tab 漏闭合]]\n内容\n无 [[/tabview]]"},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		fmt.Printf("\n=== %s ===\nIN : %q\nOUT: %s\n", c.label, c.in, out)
	}
}
