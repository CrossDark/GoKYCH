// template.typ — GoKYCH typst 文章默认页面模板
//
// 命名说明:typst 里的 `preview` 是 `@preview/...` 命名空间(放 unstable 包
// 的地方,例如 `@preview/tufted:0.1.1`),跟本文件无关。本文件是稳定的
// 页面模板,所以取名 `template.typ`,里面提供 `template(content)` 入口。
//
// 用法(用户在 typst 文章顶部):
//   #import "template.typ": template, hl, callout
//   #template[ = 标题 正文 ... ]
//
// 也可以单独 import 辅助函数 `#hl[重点]` / `#callout(title: "提示")[...]`。
//
// 这个文件由 Go 后端在启动时从 `embed.FS` 物化到 workspace dir
// (默认 `data/typst/`);首次启动会创建,之后用户可以修改本地的副本
// 而不会被覆盖。物化逻辑只在目标文件不存在时写入,见
// `internal/typst/typst.go` 的 `SetWorkspaceDir` / `ensureWorkspace`。

// ── 字体链(跨平台,覆盖 macOS / Linux / Windows) ──
//
// typst 的 `set text(font: ...)` 列表是 *per-glyph fallback*:对每个字符
// typst 在列表里找第一个有该字形的字体。所以列得越全,跨平台越鲁棒 —
// 任意一个平台只装其中一两个,typst 都能找到能渲染的字体,不会显示
// 成 missing-glyph 方块。
//
// 字体来源对应关系:
//   - macOS 自带:    PingFang SC / Hiragino Sans GB / Songti SC / STSong /
//                    Hiragino Mincho ProN / Menlo
//   - Linux 装:      fonts-noto-cjk 提供 Noto Serif CJK SC / Noto Sans CJK SC
//                    / Source Han Serif SC(Adobe 名);Sarasa Gothic SC
//                    / WenQuanYi Zen Hei 是用户/镜像站常装的备选
//   - Windows 自带:  SimSun(宋体)/ SimHei(黑体)/ Microsoft YaHei / Consolas
//   - 任意平台兜底: Linux Libertine / Liberation Serif / Liberation Sans
//                    / DejaVu Sans Mono(等宽)
//
// 字符类别:
//   `cjk-serif`  / `cjk-sans` — 中日韩
//   `latin-serif` / `latin-sans` — 拉丁字母
//   `mono` — 等宽(代码块);内含 mono 字体的 CJK 变体做兜底,因为 typst
//           渲染 typst 源码示例时经常出现 `中文 + ASCII` 混排,如果
//           字体没有 CJK 字形,中文就显示成方块

/// 衬线中文 — 正文首选
#let cjk-serif = (
  "Noto Serif CJK SC",      // Linux (fonts-noto-cjk)
  "Source Han Serif SC",    // Adobe Source Han
  "Songti SC",              // macOS 宋体
  "STSong",                 // macOS 备选
  "SimSun",                 // Windows 宋体
  "Hiragino Mincho ProN",   // macOS 衬线
  "Sarasa Gothic SC",       // 跨平台用户常装
  "WenQuanYi Zen Hei",      // Linux 备选
)

/// 无衬线中文 — heading 偏好
#let cjk-sans = (
  "Noto Sans CJK SC",       // Linux
  "Source Han Sans SC",     // Adobe
  "PingFang SC",            // macOS
  "Microsoft YaHei",        // Windows
  "Hiragino Sans GB",       // macOS
  "SimHei",                 // Windows 黑体
  "Sarasa Gothic SC",       // 跨平台
  "WenQuanYi Micro Hei",    // Linux 备选
)

/// 拉丁字母衬线 fallback
#let latin-serif = (
  "Linux Libertine",
  "Liberation Serif",
  "Times New Roman",
  "Times",
)

/// 拉丁字母无衬线 fallback
#let latin-sans = (
  "Liberation Sans",
  "Helvetica",
  "Arial",
  "Helvetica Neue",
)

/// 等宽(代码块)— 必须含 CJK 兜底,否则 typst 源码示例里的中文注释
/// 会显示成方块
#let mono = (
  "JetBrains Mono",
  "Fira Code",
  "Source Code Pro",
  "SF Mono",                // macOS
  "Menlo",                  // macOS
  "Consolas",               // Windows
  "DejaVu Sans Mono",       // Linux
  "Liberation Mono",        // Linux
  "Sarasa Mono SC",         // 跨平台(mono 字体的完整 CJK 变体)
  "Sarasa Mono Slab SC",    // 同上(Slab 变体)
  "WenQuanYi Zen Hei Mono", // Linux 备选
)

/// 正文默认字体 — CJK 衬线 + Latin 衬线 fallback。typst 会按字符在
/// 列表里选,所以这个顺序就是优先级;同一字符类别(CJK / Latin)内
/// 的顺序则按"平台覆盖广度"排:跨平台开源的 Noto 最先,macOS 专属次之。
#let body-font = cjk-serif + latin-serif

/// heading 字体 — CJK 无衬线
#let heading-font = cjk-sans + latin-sans

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
    font: body-font,
    size: 11pt,
    lang: "zh",
  )
  set par(justify: true, leading: 0.65em, first-line-indent: 2em)
  doc
}

// ── 标题样式 ──

#let heading-setup(doc) = {
  set heading(numbering: "1.")
  // heading 显式用无衬线(CJK + Latin),覆盖 text-setup 的衬线默认。
  // 不用 `set text(...)` 是因为它会影响所有后续内容;改成 show rule
  // 只作用于 heading 自身。
  show heading: it => {
    set text(font: heading-font)
    if it.level == 1 {
      set text(18pt, weight: "bold")
      block(above: 1.5em, below: 0.8em, it)
    } else if it.level == 2 {
      set text(14pt, weight: "bold")
      block(above: 1.2em, below: 0.6em, it)
    } else if it.level == 3 {
      set text(12pt, weight: "bold")
      block(above: 1em, below: 0.4em, it)
    } else {
      set text(12pt, weight: "bold")
      block(above: 0.8em, below: 0.4em, it)
    }
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
    // mono 列表里内含 CJK 兜底(Sarasa Mono SC 等),避免 typst 源码示例
    // 里的中文注释 / 字符串在 PDF 里显示成方块
    set text(font: mono, size: 9pt)
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
      text(font: mono, size: 0.9em, it),
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

/// `template(content)` — 把内容包上默认页面 + 样式。用户在文章顶部
/// 写 `#import "template.typ": template` 然后 `#template[= 标题 ...]`
/// 就能得到完整排版好的 PDF / HTML。
#let template(content) = {
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
