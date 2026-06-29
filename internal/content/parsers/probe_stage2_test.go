package parsers

import (
	"fmt"
	"testing"
)

// TestProbeStage2 is a temporary diagnostic that
// prints the rendered output for each Stage 2 input.
// Used while iterating on tests / fixes; safe to keep
// in the suite (it always passes — it's a probe, not
// an assertion).
func TestProbeStage2(t *testing.T) {
	cases := []struct {
		label string
		in    string
	}{
		{"SmartQuote_em-dash", "前后 -- 中间"},
		{"SmartQuote_apos", "it's a test"},
		{"SmartQuote_dquote", "他说 ``你好''"},
		{"SmartQuote_ellipsis", "等等..."},
		{"Blockquote_cont", "> 行1 _\n行2 结束"},
		{"DefList", ": A : 1\n: B : 2\n: 续 : 第二行\n"},
		{"FloatTOC", "++ 一级\n[[f<toc]]\n++ 二级\n[[f>toc]]"},
		{"Collapsible_folded_no", "[[collapsible folded=\"no\"]]\n内容\n[[/collapsible]]"},
		{"Collapsible_default", "[[collapsible]]\n内容\n[[/collapsible]]"},
		{"FootnoteBlock", "正文 [1] [[footnote]]脚注 1[[/footnote]] [[footnoteblock title=\"我的\"]]"},
		{"AdvList", "[[ul class=\"my\"]]\n[[li]]item 1[[/li]]\n[[li]]item 2\n[[ul]]\n[[li]]n 1[[/li]]\n[[/ul]]\n[[/li]]\n[[/ul]]"},
		{"UserMention", "[[user Alice]] [[*user Bob Smith]] [[user]]"},
	}
	for _, c := range cases {
		out := RenderWikidot(c.in)
		fmt.Printf("\n=== %s ===\nIN : %q\nOUT: %s\n", c.label, c.in, out)
	}
}
