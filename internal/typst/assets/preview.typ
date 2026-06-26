// preview.typ — GoKYCH typst 文章默认样式模块
//
// 用户在 typst 文章里 `#import "preview.typ": preview` 后,可以用
// `#preview[ ...正文... ]` 把内容裹上页面布局和默认样式,也可以单独
// `import` 模块里的辅助函数(标题、代码块、引用、高亮等)。
//
// 这个文件由 Go 后端在启动时从 `embed.FS` 物化到 workspace dir
// (默认 `data/typst/`) ;首次启动会创建,之后用户可以修改本地的副本
// 而不会被覆盖。物化逻辑只在目标文件不存在时写入,见
// `internal/typst/typst.go` 的 `init()`。

// ── 页面布局(只对 PDF 输出有效;HTML 忽略 page 设置) ──

#let page-setup(doc) = {
  set page(
    paper: "a4",
    margin: (x: 2.5cm, y: 3cm),
    header: context {
      if counter(page).get().first() > 1 [
        #set text(8pt, gray)
        #h(1fr)
        GoKYCH
      ]
    },
    footer: context {
      set text(8pt, gray)
      counter(page).display("1 / 1", both: true)
      h(1fr)
    },
  )
  doc
}

// ── 文本 / 段落默认样式 ──

#let text-setup(doc) = {
  set text(
    font: ("Noto Serif CJK SC", "Source Han Serif SC", "Noto Sans CJK SC", "Linux Libertine"),
    size: 11pt,
    lang: "zh",
  )
  set par(justify: true, leading: 0.65em, first-line-indent: 2em)
  doc
}

// ── 标题样式 ──

#let heading-setup(doc) = {
  set heading(numbering: "1.")
  show heading.where(level: 1): it => {
    set text(18pt, weight: "bold")
    block(above: 1.5em, below: 0.8em, it)
  }
  show heading.where(level: 2): it => {
    set text(14pt, weight: "bold")
    block(above: 1.2em, below: 0.6em, it)
  }
  show heading.where(level: 3): it => {
    set text(12pt, weight: "bold")
    block(above: 1em, below: 0.4em, it)
  }
  doc
}

// ── 代码块 / 行内代码样式 ──

#let code-setup(doc) = {
  show raw.where(block: true): block.with(
    fill: luma(245),
    inset: 10pt,
    radius: 4pt,
    width: 100%,
  )
  show raw.where(block: true): it => {
    set text(font: ("JetBrains Mono", "Fira Code", "Source Code Pro"), size: 9pt)
    block(
      fill: luma(245),
      inset: 10pt,
      radius: 4pt,
      width: 100%,
      it,
    )
  }
  show raw.where(block: false): it => {
    box(
      fill: luma(245),
      inset: (x: 3pt, y: 0pt),
      outset: (y: 2pt),
      radius: 2pt,
      text(font: ("JetBrains Mono", "Fira Code", "Source Code Pro"), size: 0.9em, it),
    )
  }
  doc
}

// ── 链接样式 ──

#let link-setup(doc) = {
  show link: it => {
    set text(fill: rgb("#1d4ed8"))
    underline(it)
  }
  doc
}

// ── 公开 API ──

/// `preview(content)` — 把内容包上默认页面 + 样式。用户在文章顶部
/// 写 `#import "preview.typ": preview` 然后 `#preview[= 标题 ...]`
/// 就能得到完整排版好的 PDF / HTML。
#let preview(content) = {
  page-setup(
    text-setup(
      heading-setup(
        code-setup(
          link-setup(
            content,
          ),
        ),
      ),
    ),
  )
}

/// `hl(content)` — 高亮一段文字(浅黄底)。适合在正文里标重点。
#let hl(content) = box(
  fill: rgb("#fef3c7"),
  inset: (x: 2pt, y: 0pt),
  outset: (y: 2pt),
  radius: 2pt,
  content,
)

/// `callout(title: "提示", content)` — 引用块,适合注释 / 警告 / 提示。
#let callout(title: "提示", content) = block(
  fill: rgb("#eff6ff"),
  stroke: (left: 3pt + rgb("#3b82f6")),
  inset: 10pt,
  radius: (right: 4pt),
  width: 100%,
  [
    #set text(weight: "bold", fill: rgb("#1d4ed8"))
    #title
    #v(0.3em)
    #set text(weight: "regular", fill: black)
    #content
  ],
)
