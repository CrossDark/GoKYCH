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

- `*粗体*` / `_斜体_` / `#underline[下划线]` / `#strike[删除线]`
- 行内代码:用反引号包围的内容 typst 不会当作 mark 解析
- 链接: `#link("https://example.com")[链接文字]`,或直接写 URL
- 上标/下标: `x^2` / `H_2O`

== 列表

无序:

- 第一项
- 第二项
- 第三项

有序:

+ 第一步
+ 第二步
+ 第三步

定义列表(用第一列加粗的双列表格模拟):

#table(
  columns: (auto, 1fr),
  inset: 4pt,
  [*Term*], [定义内容],
  [*Markdown*], [轻量标记语言,适合普通文章],
  [*Typst*], [可编程排版系统,适合学术/技术文档],
)

== 代码块

typst 的代码块用三个反引号围起来,反引号后跟语言名:

```python
def hello():
    print("world")
```

```go
func main() {
    fmt.Println("hello, typst!")
}
```

`template.typ` 默认会:

- 深色背景 + 等宽字体
- 圆角边框 + 轻微阴影
- JetBrains Mono / Fira Code / Source Code Pro 字体链
- HTML 端由 syntax-highlighter 做客户端高亮（只在存在代码块时按需加载）

== 辅助函数:highlight

`hl[重点]` 给一段文字加浅黄底色高亮,适合在正文里标关键词。

这里有一个 #hl[重要概念] 需要读者注意。也可以写 #hl[多行内容 但 hl 不能嵌套块级元素]。

== 辅助函数:callout

`callout` 是引用块,可以用来写提示 / 警告 / 注意:

#callout(title: "提示")[
  这里是提示内容,左侧有蓝色竖线标识。适合放补充说明。
]

#callout(title: "注意")[
  注意内容用同样的语法,只是标题文字不同。
]

#callout(title: "警告")[
  需要读者特别留意的内容放在这里。
]

== 嵌入图片

图片文件需要先在「后台 - 文件管理」上传,然后复制显示的路径,
直接粘贴到 typst 中即可 — 系统会自动处理路径,无需手动修改:

```typst
// 方式 1:复制后台显示的路径(推荐,前导斜杠会自动处理)
#image("/uploads/2024/photo.jpg", width: 80%)

// 方式 2:工作区相对路径(去掉前导斜杠)
#image("uploads/2024/photo.jpg", width: 60%)
```

#callout(title: "路径说明")[
  - 单引号和双引号都支持:`#image('/uploads/a.jpg')` 和 `#image("/uploads/a.jpg")` 等价
  - 外部 URL 直接写完整地址即可:`#image("https://example.com/photo.jpg")`
  - 头像文件在 `/avatars/` 下,用法相同:`#image("/avatars/avatar_abc.jpg", width: 30pt)`
  - 支持 PNG/JPG/SVG/PDF/GIF 格式
]

也可以使用命名参数:

```typst
#image(path: "/uploads/diagram.png", width: 80%, fit: "contain")
```

== 导入其他文件

除了 `#image`,所有 typst 文件引用函数都支持 `/uploads/` 和 `/avatars/` 路径。

=== 导入上传的 Typst 模块/库

先在后台上传你的 `.typ` 文件(如 `my-lib.typ`),然后:

```typst
// 导入文件中所有定义
#import "/uploads/my-lib.typ"

// 只导入特定函数/变量
#import "/uploads/my-lib.typ": my-function, my-template, my-variable

// 执行文件中的代码但不导入命名空间(相当于 include)
#include "/uploads/header.typ"
#include "/uploads/footer.typ"
```

=== 读取上传的数据文件

```typst
// 读取 CSV 数据并渲染为表格
#let data = csv("/uploads/data.csv")
#table(columns: 3, ..data.flatten())

// 引用 BibTeX 书目
#bibliography("/uploads/refs.bib", style: "apa")

// 读取 JSON/YAML 等数据文件
#let config = json("/uploads/config.json")
```

#callout(title: "支持的文件类型")[
  - `#image()` — PNG / JPG / SVG / PDF / GIF
  - `#read()` / `csv()` / `json()` / `yaml()` — 纯文本和结构化数据
  - `#bibliography()` — BibTeX (`.bib`)
  - `#import` / `#include` — Typst 源文件 (`.typ`)
  - 所有函数都支持 `/uploads/` 和 `/avatars/` 前缀路径
]

=== 引用其他网站文章(跨文章导入)

使用 `@slug` 语法引用网站上其他 typst 类型的文章:

```typst
// 导入整篇文章的输出内容
#include "@shared-macros"

// 导入另一篇文章中定义的函数/模板
#import "@my-library": my-function, my-mixin
```

#callout(title: "依赖自动管理")[
  - 被 `@import` 的文章如果更新,依赖它的所有文章会自动重新编译(级联失效)
  - 系统会检测循环引用,发现后返回明确的编译错误
  - 被引用文章中使用的 `/uploads/` 图片、`#import` 库文件也会被正确解析
  - 依赖链中的文章路径重写自动生效,无需手动处理
]

== 公式

typst 原生支持数学公式,行内用一对 `$`,行间用独立的 `$` 块:

欧拉恒等式 $e^(i pi) + 1 = 0$ 是数学里最美的公式之一。

高斯积分:

$
integral_0^infinity e^(-x^2) dif x = sqrt(pi) / 2
$

矩阵:

$
mat(
  1, 2, dots, 10;
  2, 4, dots, 20;
  dots.v, dots.v, dots.down, dots.v;
  10, 20, dots, 100;
)
$

求和与极限:

$
lim_(x->0) (sin x)/x = 1,     sum_(n=1)^infinity 1/n^2 = pi^2/6
$

公式中的字体大小、符号风格由 template.typ 自动设置,作者无需额外配置。

== 表格

#table(
  columns: (auto, 1fr, auto),
  align: (left, left, right),
  inset: 6pt,
  stroke: 0.5pt,
  [*列1*], [*列2*], [*列3*],
  [A],  [第一行内容], [1],
  [C],  [第二行内容], [2],
)

```typst
#table(
  columns: (auto, 1fr, auto),
  align: (left, left, right),
  [*列1*], [*列2*], [*列3*],
  [A],  [第一行], [1],
  [C],  [第二行], [2],
)
```

复杂表格可以用 `table.cell` 跨行跨列,或通过 `#figure()` 加图注:

```typst
#figure(
  table(columns: 2, [*参数*], [*说明*], [width], [图片宽度]),
  caption: [表格说明文字]
)
```

== 章节编号

`template.typ` 默认给 H1–H3 启用了 `1.1` / `1.1.1` 风格的自动编号
(在 PDF 里尤其有用)。如果某个标题不想参与编号,加一个 `<nope>` 标签:

```typst
= 参与编号的标题
== 1.1 小节

= 不被编号的标题 <nope>
```

== PDF 导出

访问文章时,URL 后加 `/pdf` 即可下载 PDF 版本
(如 `https://example.com/typst/my-article/pdf`)。

- PDF 使用 A4 纸张,默认 2.5cm 边距
- 页眉显示网站标题,页脚居中显示页码
- HTML 中设置的页面大小(`set page(...)`)在网页版会被忽略,只在 PDF 中生效
- PDF 中图片会被嵌入文件,离线阅读不丢失

== 失败怎么办

发布时如果 typst 编译失败,Go 后端会:

- *create 模式*:删除刚创建的文章(回滚),返回 400,slug 释放,
  错误信息包含 typst CLI 的完整输出
- *update 模式*:把 title / content 还原到上次保存的版本,返回 400,
  编辑器显示旧状态 — 不会出现「内容改了但渲染不到」的撕裂态

要在命令行手动验证语法:

```bash
cd /opt/gokych/data/typst
typst compile article-<id>.typ output.html
```

常见失败原因:

+ 漏写 `#template[ ... ]` 包裹
+ `#import` 路径不对(typo、文件不存在、slug 写错)
+ `#image` 路径不存在(检查后台文件管理中是否已上传)
+ typst 语法错(漏 `[]` / `()` / `{}` 配对,中文标点误用)
+ `@import` 循环依赖(A 导入 B,B 又导入 A)
+ typst 版本过旧,使用了新版本才有的函数(建议升级到最新版 typst CLI)

#callout(title: "调试小技巧")[
  把 typst CLI 报错中提到的 `.typ` 文件名中的数字 ID 对应到文章 ID,
  可以在后台 URL 中找到对应的文章。例如 `article_42.typ` 对应
  ID=42 的文章。
]

== 高级:自定义模板

Go 后端只在 `template.typ` *不存在* 时才物化内置版本。
第一次启动后,你可以直接编辑服务器上的文件来自定义样式:

- 路径:`<DATA_DIR>/typst/template.typ`(生产环境通常是 `/opt/gokych/data/typst/template.typ`)
- 可以修改字体、颜色、页边距、标题样式、代码块主题
- 可以添加自己的辅助函数(如自定义 callout 样式、图表宏等)
- 修改后保存即可,新发布/重编译的文章会使用新模板
- 旧文章的缓存不会自动失效 — 需要重新保存触发重编译

== 性能

发布时编译,访问者读缓存,无需为性能担心 — 一次发布之后所有
读者都享受相同的速度。

- 编译并发上限:4(同时最多 4 个 typst 进程)
- 编译超时:30 秒(单篇文章,HTML+PDF 并行编译)
- HTML 和 PDF 同时编译(而非串行),墙钟时间约为 max(t_html, t_pdf)
- 依赖更新时自动级联重编译,Worker 单线程队列,避免 typst 进程雪崩
- 文章详情页的数据库查询(errgroup 并行)与 typst 编译互不阻塞

== 内容长度建议

- 单篇文章建议控制在 5000 字以内,编译时间通常在 1–3 秒
- 超长文档(10000+ 字、大量图片)可能接近 30s 超时,建议拆分为系列文章
- 图片建议先压缩到 200KB 以内再上传,减少编译内存占用和 PDF 文件大小

]
