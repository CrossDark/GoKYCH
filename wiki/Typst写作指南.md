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

### 引用其他 Typst 文章（跨文章导入）

使用 `@slug` 语法引用网站上其他 typst 文章的内容：

```typst
// 导入整篇文章
#include "@shared-macros"

// 导入特定函数/变量
#import "@my-library": my-function, my-template
```

被导入的文章也可以继续 `@import` 其他文章（依赖会自动递归解析），
系统会自动检测循环引用并报错。

### 导入上传的 `.typ` 文件

通过后台上传 `.typ` 库文件后，和图片一样直接引用路径即可：

```typst
// 导入上传的 typst 库文件
#import "/uploads/my-macros.typ": my-function, another-func

// 包含上传的片段文件
#include "/uploads/header.typ"

// 读取上传的数据文件（如 CSV、BibTeX）
#let data = csv("/uploads/data.csv")
#bibliography("/uploads/refs.bib")
```

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
  columns: 3,
  [*列1*], [*列2*], [*列3*],
  [A],     [B],     [C],
  [D],     [E],     [F],
)
```

---

## PDF 导出

访问文章时，URL 后加 `/pdf` 即可下载 PDF 版本（如 `/typst/my-article/pdf`）。
PDF 使用 A4 纸张，默认 2.5cm/3cm 边距，页眉显示网站名，页脚显示页码。

> PDF 页面设置只对 PDF 输出有效，网页版（HTML）会自动忽略 `set page(...)` 配置。

---

## 注意事项

1. **所有内容放在 `#template[...]` 内**：否则中文字体、代码块样式等不会生效
2. **编译超时 30 秒**：大型文档（几百页、大量图片）可能超时，建议分章节发布
3. **文件格式**：`#image()` 支持 PNG/JPG/SVG/PDF；`#read()` 支持纯文本；
   `#bibliography()` 支持 BibTeX（`.bib`）
4. **并发编译上限 4**：同时最多编译 4 篇文章，超出自动排队
5. **首次发布编译**：文章保存时会触发编译，读者访问时读取缓存，无需等待编译
6. **缓存失效**：当被依赖的文章（`@slug`）更新时，依赖方会自动重新编译

---

## 更多资源

- [Typst 官方教程](https://typst.app/docs/tutorial/)
- [Typst 语法参考](https://typst.app/docs/reference/)
- [Typst 中文社区](https://typst.cn/)
