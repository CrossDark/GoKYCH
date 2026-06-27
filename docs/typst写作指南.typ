// typst写作指南.typ
//
// 这是 GoKYCH 网站「作者必读」 — 教你用 typst 写一篇文章,以及怎么用
// 我们内置的 template.typ 提供的辅助函数(hl / callout 等)。这份
// 文档本身也是一份 typst 文章源码 — 在 GoKYCH 网站后台以 typst 类型
// 发布后,可以直接被渲染成 HTML / PDF,跟用户写的内容走同一条
// precompile 流水线。
//
// 一些约定:
//   - = 标题    → typst 一级标题(H1)
//   - == 标题   → typst 二级标题(H2)
//   - 正文段落用空行分隔
//   - 代码块用三个反引号围起来,反引号后跟语言名
//   - 行内代码用 ` 单引号

#import "template.typ": template, hl, callout

#template[

= GoKYCH Typst 写作指南

Typst 是 GoKYCH 5 种文章类型之一(`md` / `wikidot` / `html` / `bbcode` / `typst`),
适合需要精确排版、富排版元素(代码块、引用块、图注、表格、公式)的场景。
Typst 源文件会被 Go 后端在「发布时」编译成 HTML 和 PDF 并缓存,
用户访问时只读缓存 — 不会有 typst CLI 进程开销。

#callout(title: "本指南是活的")[
  本文档本身是一份 typst 源码 — 在 GoKYCH 网站后台选择 `typst` 类型
  发布后,可以直接渲染。看到的版式就是 typst 实际输出的样子。
]

== 最小可发布文章

```typst
#import "template.typ": template

#template[
  = 标题
  正文段落...
]
```

`#import` 导入 `template.typ` 提供的 `template` 函数;`#template[ ... ]`
把内容包上默认页面布局和样式。*没有 `template[]` 包裹的内容不会被
正确排版* — 没有字体设置、没有标题编号、没有代码块样式。

== 标题

```typst
= 一级标题(H1)
== 二级标题(H2)
=== 三级标题(H3)
```

级别 1–3 在 `template.typ` 里有显式字号;更深级别会落到 typst 默认。

== 文本格式

- `*粗体*` / `_斜体_` / `#underline[下划线]`
- 行内代码:用反引号包围的内容 typst 不会当作 mark 解析
- 链接: `#link("https://example.com")[链接文字]`

== 列表

无序:

- 第一项
- 第二项
- 第三项

有序:

+ 第一步
+ 第二步
+ 第三步

== 代码块

typst 的代码块用三个反引号围起来,反引号后跟语言名:

```python
def hello():
    print("world")
```

`template.typ` 默认会:

- 灰色背景
- 等宽字体(JetBrains Mono / Fira Code / Source Code Pro)
- 圆角边框

== 辅助函数:highlight

`hl[重点]` 给一段文字加浅黄底色高亮,适合在正文里标关键词。

这里有一个 #hl[重要概念] 需要读者注意。

== 辅助函数:callout

`callout` 是引用块,可以用来写提示 / 警告 / 注意:

#callout(title: "提示")[
  这里是提示内容,左侧有蓝色竖线标识。
]

#callout(title: "注意")[
  注意内容用同样的语法,只是标题文字不同。
]

#callout(title: "警告")[
  警告用红色会更醒目 — 但当前 template 还没加,先用蓝色顶一下。
]

== 嵌入图片

图片文件需要先在「文件管理」上传,得到 URL 后:

```typst
#image("/uploads/xxx.png", width: 80%)
```

相对路径会从 typst workspace 目录(`<DATA_DIR>/typst/`)开始解析,
所以如果图片在 workspace 根目录可以直接 `#image("foo.png")`。
跨域部署(前后端不同源)要写绝对 URL。

== 公式

typst 原生支持数学公式,行内用一对 `$`,行间用单 `$` 起止:

欧拉恒等式 $e^(i pi) + 1 = 0$ 是数学里最美的公式之一。

$ integral_0^infinity e^(-x^2) dif x = sqrt(pi) / 2 $

== 表格

```typst
#table(
  columns: (auto, 1fr, auto),
  align: (left, left, right),
  [列1], [列2], [列3],
  [A],  [B],  [1],
  [C],  [D],  [2],
)
```

== 章节编号

`template.typ` 默认给 H1 启用了 `1.1` / `1.1.1` 风格的自动编号
(在 PDF 里尤其有用)。如果某个标题不想参与编号,加一个 `nonumber` 类:

```typst
= 不会被编号的标题 <nope>
```

== 失败怎么办

发布时如果 typst 编译失败,Go 后端会:

- *create 模式*:删除刚创建的文章(回滚),返回 400,slug 释放,
  错误信息包含 typst CLI 的输出
- *update 模式*:把 title / content 还原到上次保存的版本,返回 400,
  编辑器显示旧状态 — 不会出现「内容改了但渲染不到」的撕裂态

要在命令行手动验证语法:

```bash
cd /opt/gokych/data/typst
typst compile your-article.typ your-article.html
```

常见失败原因:

+ 漏写 `#template[ ... ]` 包裹
+ `#import` 路径不对(typo 或目录层级错)
+ `#image` 路径不存在
+ typst 语法错(漏 `[]` / `()` / `{}` 配对)

== 高级:修改本地 template

Go 后端只在 `template.typ` 不存在时物化,本地修改不会被覆盖。
可以改字体、颜色、页边距、加新辅助函数等。

workspace 目录在生产是 `/opt/gokych/data/typst/`,由
`cfg.App.DataDir + "/typst"` 决定(参 `cmd/gokych/main.go` 的
`typst.SetWorkspaceDir` 调用)。

== 性能

发布时编译,访问者读缓存,无需为性能担心 — 一次发布之后所有
读者都享受相同的速度。typst 一次编译吃 1 个 semaphore slot,
最多并发 4 个,30s timeout(见「网站 typst 实现原理」文档)。

]
