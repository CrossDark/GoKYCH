// Demo renderer: takes a wikidot source file, runs it through
// the wikidot parser, wraps in minimal page chrome, writes
// the result to an HTML file for browser verification.
//
// Usage:
//   go run ./examples/wikidot-demo-render -src input.wiki -out output.html
//   go run ./examples/wikidot-demo-render -out output.html < input.wiki

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gokych/internal/content/parsers"
)

func main() {
	out := flag.String("out", "/tmp/wikidot-render.html", "output HTML path")
	src := flag.String("src", "", "wikidot source file (default: stdin)")
	flag.Parse()

	var source string
	if *src != "" {
		b, err := os.ReadFile(*src)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read source:", err)
			os.Exit(1)
		}
		source = string(b)
	} else {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read stdin:", err)
			os.Exit(1)
		}
		source = string(b)
	}

	start := time.Now()
	body := parsers.RenderWikidot(source)
	elapsed := time.Since(start)

	html := buildPage(body, source)
	if err := os.WriteFile(*out, []byte(html), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write html:", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "rendered %d source chars → %d HTML chars in %s\n",
		len(source), len(body), elapsed)
	fmt.Fprintf(os.Stderr, "output: %s\n", *out)
}

// buildPage wraps the rendered HTML body in minimal chrome so
// the page renders standalone in a browser. No external CSS /
// JS — everything is inline so the page is reachable from
// `file://` without network access.
func buildPage(body, source string) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>Wikidot Syntax Demo</title>
<style>
  body {
    font-family: -apple-system, BlinkMacSystemFont, "PingFang SC", "Segoe UI", sans-serif;
    max-width: 760px;
    margin: 40px auto;
    padding: 0 20px;
    color: #1f2937;
    background: #fafafa;
    line-height: 1.6;
  }
  h1, h2, h3, h4, h5, h6 {
    font-weight: 600;
    color: #111827;
    margin: 1.5em 0 0.5em;
    line-height: 1.3;
  }
  h1 { font-size: 28px; border-bottom: 2px solid #e5e7eb; padding-bottom: 8px; }
  h2 { font-size: 22px; border-bottom: 1px solid #e5e7eb; padding-bottom: 4px; }
  h3 { font-size: 18px; }
  h4 { font-size: 16px; }
  code {
    font-family: ui-monospace, "SF Mono", Menlo, monospace;
    background: #f3f4f6;
    padding: 1px 5px;
    border-radius: 3px;
    font-size: 0.9em;
  }
  pre {
    background: #1f2937;
    color: #e5e7eb;
    padding: 14px 18px;
    border-radius: 6px;
    overflow: auto;
    font-size: 13px;
    line-height: 1.5;
  }
  pre code { background: transparent; padding: 0; color: inherit; }
  blockquote {
    border-left: 3px solid #94a3b8;
    margin: 1em 0;
    padding: 0.5em 1em;
    background: #f8fafc;
    color: #475569;
  }
  a.new-tab::after { content: " ↗"; font-size: 0.75em; color: #6b7280; vertical-align: super; }
  table.wiki-table { border-collapse: collapse; width: 100%; margin: 12px 0; }
  table.wiki-table th, table.wiki-table td {
    border: 1px solid #cbd5e1; padding: 8px 12px; text-align: left;
  }
  table.wiki-table th { background: #f1f5f9; font-weight: 600; }
  .wikidot-toc {
    background: #fef3c7;
    border: 1px solid #fbbf24;
    border-radius: 6px;
    padding: 12px 16px;
    margin: 16px 0;
  }
  .wikidot-toc-title { font-weight: 600; color: #92400e; margin-bottom: 6px; font-size: 14px; }
  .wikidot-toc ul { margin: 0; padding-left: 20px; font-size: 14px; }
  .wikidot-toc a { color: #92400e; text-decoration: none; }
  .wikidot-toc a:hover { text-decoration: underline; }
  .wikidot-image-wrap {
    display: inline-block;
    padding: 6px;
    border: 1px dashed #d1d5db;
    border-radius: 4px;
    margin: 8px 4px;
    background: #f9fafb;
    font-size: 12px;
    color: #6b7280;
  }
  .wikidot-image-center { display: block; text-align: center; margin: 12px auto; }
  .wikidot-image-left { float: left; margin: 0 12px 12px 0; }
  .wikidot-image-right { float: right; margin: 0 0 12px 12px; }
  .wikidot-image-floatleft { float: left; margin: 0 16px 12px 0; max-width: 45%; }
  .wikidot-image-floatright { float: right; margin: 0 0 12px 16px; max-width: 45%; }
  .wikidot-toc-float-left, .wikidot-toc-float-right { max-width: 50%; padding: 12px 16px; }
  .wikidot-toc-float-left { float: left; margin-right: 16px; }
  .wikidot-toc-float-right { float: right; margin-left: 16px; }
  .source-block {
    background: #f9fafb;
    border: 1px solid #e5e7eb;
    border-radius: 6px;
    padding: 10px 14px;
    margin: 10px 0;
    font-family: ui-monospace, monospace;
    font-size: 12.5px;
    color: #374151;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .source-label {
    display: inline-block;
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.06em;
    color: #6b7280;
    text-transform: uppercase;
    margin: 0 0 4px;
  }
  .demo-card {
    background: white;
    border: 1px solid #e5e7eb;
    border-radius: 12px;
    padding: 24px 28px;
    margin: 20px 0;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  }
  .demo-card h2 { margin-top: 0; }
  .badge {
    display: inline-block;
    font-size: 10px;
    font-weight: 700;
    padding: 2px 8px;
    border-radius: 4px;
    background: #ddd6fe;
    color: #5b21b6;
    margin-left: 8px;
    vertical-align: middle;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .badge.new { background: #bbf7d0; color: #166534; }
  .clearfix::after { content: ""; display: table; clear: both; }
  aside.wikidot-note {
    display: inline-block;
    background: #fef9c3;
    border-left: 3px solid #facc15;
    padding: 6px 12px;
    border-radius: 0 4px 4px 0;
    margin: 0 4px;
  }
  details.wiki-collapsible {
    border: 1px solid #e5e7eb;
    border-radius: 6px;
    padding: 8px 12px;
    margin: 12px 0;
    background: #f9fafb;
  }
  details.wiki-collapsible summary { cursor: pointer; font-weight: 600; }
  .wikidot-indent { padding-left: 1.5em; border-left: 1px dashed #cbd5e1; margin-left: 4px; }
  .wikidot-tabview { border: 1px solid #e5e7eb; border-radius: 8px; margin: 16px 0; }
  .wikidot-tab-nav { list-style: none; padding: 0; margin: 0; display: flex; border-bottom: 1px solid #e5e7eb; background: #f9fafb; border-radius: 8px 8px 0 0; }
  .wikidot-tab-tab { padding: 8px 16px; cursor: pointer; }
  .wikidot-tab-tab.active { background: white; border-bottom: 2px solid #2563eb; color: #2563eb; }
</style>
</head>
<body>
<div class="demo-card">
<h1>Wikidot Parser Demo <span class="badge new">P1 Round 3</span></h1>
<p>下面是同一段 wikidot 源码经过 <code>gokych/internal/content/parsers.RenderWikidot()</code> 实际渲染的 HTML 输出。每张卡片对应一项新语法。</p>
</div>
`)
	sb.WriteString(body)
	sb.WriteString(`
<div class="demo-card">
<h2>源 (source) vs 输出 (HTML)</h2>
<details>
<summary>查看完整 wikidot 源码</summary>
<div class="source-block">`)
	sb.WriteString(htmlEscape(source))
	sb.WriteString(`</div>
</details>
</div>
</body>
</html>`)
	return sb.String()
}

func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}
