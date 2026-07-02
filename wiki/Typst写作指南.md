# Typst 写作指南

GoKYCH 支持 [Typst](https://typst.app/) 作为文章格式。Typst 是一种现代化的标记语言，
比 LaTeX 简单、比 Markdown 强大，适合排版数学公式、技术文档、学术论文等。

---

## 快速开始

创建一篇新文章时，选择类型为 `typst`，在内容框中输入：

```typst
#import "template.typ": template, hl, callout

#template[
= 我的第一篇 Typst 文章

这是正文。用 `#hl[重点内容]` 来高亮文字。

#callout(title: "提示")[
这是一个提示框。
]

== 二级标题

正文内容...
]
```

> **重要**：所有内容必须放在 `#template[...]` 的方括号内，才能正确应用默认样式
> （中文字体、代码高亮、标题样式等）。

---

## 基础语法

### 标题

```typst
= 一级标题
== 二级标题
=== 三级标题
==== 四级标题
```

### 段落与换行

- 段落之间用空行分隔
- 单个换行不分段（和 Markdown 类似）
- 用 `#parbreak()` 或两个空行强制分段

### 强调

```typst
*粗体*    _斜体_    `行内代码`
#underline[下划线]   #strike[删除线]
```

### 列表

```typst
- 无序列表项 A
- 无序列表项 B
  - 嵌套项

+ 有序列表项 1
+ 有序列表项 2
```

### 代码块

使用三反引号（和 Markdown 一样）或 typst 原生语法：

````typst
```go
func main() {
    fmt.Println("hello")
}
```
````

### 链接

```typst
#link("https://example.com")[链接文字]
// 简短写法：
https://example.com
```

### 上标与下标

```typst
x^2     H_2O     e^(-x^2)
```

---

## 图片与文件引用

### 引用网站上传的图片

你可以通过后台「文件管理」上传图片，然后复制显示的路径，直接粘贴到 typst 中：

```typst
#image("/uploads/2024/photo.jpg")
#image("/uploads/diagram.png", width: 80%)
#image("/avatars/avatar_abc123.jpg", width: 30pt)
```

系统会自动处理路径，无需手动去掉开头的 `/`。同时支持单引号和双引号。

也可以使用工作区相对路径（去掉开头的 `/`）：

```typst
#image("uploads/photo.jpg")
```

### 命名参数写法

```typst
#image(path: "/uploads/diagram.png", width: 80%, fit: "contain")
```

### 图片参数

```typst
#image("/uploads/photo.jpg", width: 60%, height: 200pt)
#image("/uploads/photo.jpg", fit: "contain")
```

### 外部图片

直接使用完整 URL 即可：

```typst
#image("https://example.com/photo.jpg")
```

---

## 导入其他 Typst 文件

除了 `#image`，所有 typst 文件引用函数都支持 `/uploads/` 和 `/avatars/` 路径。

### 引用其他 Typst 文章（跨文章导入）

使用 `@slug` 语法引用网站上其他 typst 文章的内容：

```typst
// 导入整篇文章
#include "@shared-macros"

// 导入特定函数/变量
#import "@my-library": my-function, my-template
```

被导入的文章也可以继续 `@import` 其他文章（依赖会自动递归解析），
系统会自动检测循环引用并报错。被引用文章更新时，依赖方自动级联重编译。

### 导入上传的 `.typ` 文件

通过后台上传 `.typ` 库文件后，和图片一样直接引用路径即可：

```typst
// 导入上传的 typst 库文件（全部命名空间）
#import "/uploads/my-macros.typ"

// 只导入特定函数/变量
#import "/uploads/my-macros.typ": my-function, another-func

// 包含上传的片段文件（直接嵌入内容）
#include "/uploads/header.typ"
#include "/uploads/footer.typ"
```

### 读取上传的数据文件

```typst
// 读取 CSV 数据并渲染为表格
#let data = csv("/uploads/data.csv")
#table(columns: 3, ..data.flatten())

// 引用 BibTeX 书目
#bibliography("/uploads/refs.bib", style: "apa")

// 读取 JSON/YAML 等结构化数据
#let config = json("/uploads/config.json")
```

**支持的文件类型**：

| 函数 | 支持的文件 |
|------|-----------|
| `#image()` | PNG / JPG / SVG / PDF / GIF |
| `#read()` | 纯文本 |
| `csv()` / `json()` / `yaml()` | 结构化数据 |
| `#bibliography()` | BibTeX (`.bib`) |
| `#import` / `#include` | Typst 源文件 (`.typ`) |

### 导入内置模板

网站内置了 `template.typ`，提供了默认的中文字体链和样式：

```typst
#import "template.typ": template, hl, callout
```

**辅助函数说明**：

| 函数 | 用途 | 示例 |
|------|------|------|
| `template(content)` | 应用默认页面样式（字体/标题/代码/链接） | `#template[= 标题 正文...]` |
| `hl(content)` | 高亮文字（浅黄底） | `这是 #hl[重点]` |
| `callout(title, content)` | 提示框（蓝色左边框） | `#callout(title: "注意")[内容]` |

> 你也可以通过 SSH 编辑服务器上 `data/typst/template.typ` 来自定义模板，
> 首次启动后该文件不会被覆盖。

---

## 数学公式

Typst 原生支持数学公式，语法比 LaTeX 简洁：

```typst
行内公式：$a^2 + b^2 = c^2$

行间公式：
$
integral_0^infty e^(-x^2) dx = sqrt(pi)/2
$

矩阵：
$
mat(
  1, 2, ..., 10;
  2, 4, ..., 20;
  ...
)
$
```

---

## 表格

```typst
#table(
  columns: (auto, 1fr, auto),
  align: (left, left, right),
  [*列1*], [*列2*], [*列3*],
  [A],     [B],     [C],
  [D],     [E],     [F],
)
```

复杂表格可以跨行跨列，或通过 `#figure()` 加图注：

```typst
#figure(
  table(columns: 2, [*参数*], [*说明*], [width], [图片宽度]),
  caption: [表格说明文字]
)
```

---

## 章节编号

`template.typ` 默认给 H1-H3 启用了 `1.1` / `1.1.1` 风格的自动编号
（在 PDF 里尤其有用）。如果某个标题不想参与编号，加一个 `<nope>` 标签：

```typst
= 参与编号的标题
== 1.1 小节

= 不被编号的标题 <nope>
```

---

## PDF 导出

访问文章时，URL 后加 `/pdf` 即可下载 PDF 版本（如 `/typst/my-article/pdf`）。
PDF 使用 A4 纸张，默认 2.5cm 边距，页眉显示网站标题，页脚居中显示页码。

> PDF 页面设置只对 PDF 输出有效，网页版（HTML）会自动忽略 `set page(...)` 配置。
> PDF 中图片会被嵌入文件，离线阅读不丢失。

---

## 编译失败排查

发布时如果 typst 编译失败，Go 后端会：
- **create 模式**：删除刚创建的文章（回滚），返回 400，错误信息包含 typst CLI 的完整输出
- **update 模式**：把 title/content 还原到上次保存版本，编辑器显示旧状态

常见失败原因：

1. 漏写 `#template[ ... ]` 包裹
2. `#import` 路径不对（typo、文件不存在、slug 写错）
3. `#image` 路径不存在（检查后台文件管理中是否已上传）
4. typst 语法错（漏 `[]` / `()` / `{}` 配对，中文标点误用）
5. `@import` 循环依赖（A 导入 B，B 又导入 A）
6. typst 版本过旧，使用了新版本才有的函数（建议升级到最新版 typst CLI）

> **调试小技巧**：typst CLI 报错中提到的 `article_<id>.typ` 文件名中的数字 ID
> 就是文章 ID，可在后台 URL 中找到对应文章。例如 `article_42.typ` 对应 ID=42 的文章。

---

## 自定义模板

Go 后端只在 `template.typ` 不存在时才物化内置版本。第一次启动后可以直接编辑
服务器上的文件自定义样式：

- 路径：`<DATA_DIR>/typst/template.typ`（生产环境通常是 `/opt/gokych/data/typst/template.typ`）
- 可修改字体、颜色、页边距、标题样式、代码块主题
- 可添加自己的辅助函数（如自定义 callout 样式、图表宏等）
- 修改后保存即可，新发布/重编译的文章会使用新模板
- 旧文章缓存不会自动失效 — 需要重新保存触发重编译

---

## 性能与长度建议

- 编译并发上限：4（同时最多 4 个 typst 进程）
- 编译超时：30 秒（单篇文章，HTML+PDF 并行编译）
- HTML 和 PDF 同时编译（而非串行），墙钟时间约为 max(t_html, t_pdf)
- 依赖更新时自动级联重编译，Worker 单线程队列
- 单篇文章建议控制在 **5000 字以内**，编译时间通常在 1–3 秒
- 超长文档（10000+ 字、大量图片）可能接近 30s 超时，建议拆分为系列文章
- 图片建议先压缩到 **200KB 以内** 再上传，减少编译内存占用和 PDF 文件大小

---

## 注意事项

1. **所有内容放在 `#template[...]` 内**：否则中文字体、代码块样式等不会生效
2. **编译超时 30 秒**：大型文档（几百页、大量图片）可能超时，建议分章节发布
3. **文件格式**：`#image()` 支持 PNG/JPG/SVG/PDF/GIF；`#read()` 支持纯文本；
   `#bibliography()` 支持 BibTeX（`.bib`）
4. **并发编译上限 4**：同时最多编译 4 篇文章，超出自动排队
5. **首次发布编译**：文章保存时会触发编译，读者访问时读取缓存，无需等待编译
6. **缓存失效**：当被依赖的文章（`@slug`）更新时，依赖方会自动重新编译
7. **路径自动重写**：直接复制后台文件管理器显示的路径（以 `/uploads/` 开头）即可，
   系统会自动处理为工作区相对路径

---

## 更多资源

- [Typst 官方教程](https://typst.app/docs/tutorial/)
- [Typst 语法参考](https://typst.app/docs/reference/)
- [Typst 中文社区](https://typst.cn/)
