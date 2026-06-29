package parsers

import (
	"fmt"
	"regexp"
	"testing"
)

// TestProbeRegex is a one-off that matches the reWDUL
// / reWDLIOpen regexes against known inputs to see
// whether they actually match what we expect. Used
// while debugging Stage 2 advanced-list coverage.
func TestProbeRegex(t *testing.T) {
	cases := []struct {
		pat *regexp.Regexp
		in  string
		lab string
	}{
		{reWDUL, "[[ul class=\"my\"]]", "reWDUL basic"},
		{reWDUL, "[[ul]]", "reWDUL bare"},
		{reWDLIOpen, "[[li]]", "reWDLIOpen bare"},
		{reWDLIOpen, "[[li class=\"x\" style=\"color:red\"]]", "reWDLIOpen attrs"},
		{reWDLIOpen, "[[li data-id=\"42\"]]", "reWDLIOpen data-attr"},
		{reWDLIClose, "[[/li]]", "reWDLIClose"},
		{reWDULClose, "[[/ul]]", "reWDULClose"},
		{reWDOL, "[[ol class=\"a b\"]]", "reWDOL multi-class"},
	}
	for _, c := range cases {
		if c.pat.MatchString(c.in) {
			fmt.Printf("MATCH   %s : %q\n", c.lab, c.in)
		} else {
			fmt.Printf("NO MATCH %s : %q\n", c.lab, c.in)
		}
	}
}

// TestProbeAdvList calls renderWikidotAdvancedLists
// directly (no wrap, no other phase) to see what the
// function actually produces.
func TestProbeAdvList(t *testing.T) {
	in := "[[ul class=\"my\"]]\n[[li]]item 1[[/li]]\n[[li]]item 2\n[[ul]]\n[[li]]n 1[[/li]]\n[[/ul]]\n[[/li]]\n[[/ul]]"
	out := renderWikidotAdvancedLists(in)
	fmt.Printf("\nIN : %q\nOUT: %q\n", in, out)
}
