package parsers

import (
	"fmt"
	"testing"
)

// TestProbeStage3 prints the rendered output for each
// Stage 3 input. Probe — always passes.
func TestProbeStage3(t *testing.T) {
	cases := []struct {
		label string
		in    string
	}{
		{"Divider", "上\n[[divider]]\n下"},
		{"Note", "一段话 [[note]]注意一下这条提示[[/note]] 续接"},
		{"Note_block", "[[note]]\n整段提示内容\n跨多行\n[[/note]]"},
		{"Button_external", "[[button 访问 GitHub|https://github.com]]"},
		{"Button_internal", "[[button 回首页|/]]"},
		{"Button_no_target", "[[button 占位按钮]]"},
		{"Email_block", "联系方式 [[email]]foo@example.com[[/email]] 收"},
		{"Email_tag", "或 [[email bar@example.org]]"},
		{"Email_malformed", "[[email not-an-email]]"},
		{"Code_in_p", "前面\n[[code]]\nx = 1\n[[/code]]\n后面"},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		fmt.Printf("\n=== %s ===\nIN : %q\nOUT: %s\n", c.label, c.in, out)
	}
}
