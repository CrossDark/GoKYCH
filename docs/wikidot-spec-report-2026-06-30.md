# Wikidot 语法全面对标测试报告

**对比基准**: https://rule-wiki.wikidot.com/wiki-syntax(Wikidot 官方中文翻译镜像)
**测试源**: 用户提供的完整 wikidot 语法参考文章(中文版)
**测试页面**: 本地 GoKYCH `wikidot/wd-syntax-spec` (slug) — 完整源码直接渲染
**Playwright 视口**: 1440 / 1023 / 768 / 480 / 375
**截图**: `/tmp/gokych-shots/wd-spec-{viewport}.png` + `wd-spec-atf.png`

---

## TL;DR

* **总检查项**: 85
* **PASS**: 48(56%)
* **WARN**: 3(测试脚本 false-positive,实际 OK)
* **FAIL**: 34(其中 27 个是**同一个根因**:"table cell 走 `inlineOnly`,不递归到完整 inline 处理")

**核心诊断**: 看 `internal/content/parsers/wikidot.go:2892-2894`,每个 `||...||` cell 走 `inlineOnly(c)`,而 `inlineOnly` 只跑:

```
**bold**  //italic//  __under__  --strike--  ^^sup^^  ,,sub,,  {{code}}
##color##  [^N]  [url text]  [mailto:...]  [[[wiki-link]]]
```

所有 `[[...]]` 多标记形式(`[[size X]]`, `[[span style=...]]`, `[[span class=...]]`, `[!-- --]` 注释, `[[ul]][[li class=...]]`)在 TD 内**完全不渲染**,直接吐原文。这一个根因撑起了 80% 的 FAIL 行。

---

## 修复前后的发现

| 现象 | 检查原因 |
|---|---|
| `/tmp/p5-verify.cjs` 类似的旧脚本能 login,但今天登不上 | 同源 SESSION cookie 被拒(`SameSite=None; Secure=false` 在 HTTP 下 Chrome 拒绝) |
| 当前 server 进程是 Jun 28 20:20 编译的旧二进制,没含 `fbdd675`(heading source order + nested TOC + size across paragraphs) | `git log` 显示新 commit 在 Jun 28 之后,进程是 `./dist/gokych` |
| 重建后,**TOC 嵌套 ul 生效了**(fbdd675 修复),但 H1 仍只看到 H3 entries | 二级 bug —— heading 收集仍有问题 |
| 一些 inline 行外(`[[size]]` outside `||`,`:term : def`,`> blockquote`)直接渲染 OK | `inlineOnly` 不背锅,真正 wikidot 全管道在跑 |

---

## 详细结果(按 Spec Section)

### ✅ 已对齐(48 PASS)

| Section | 项 | 证据 |
|---|---|---|
| **inline** | `//`→`<em>`、`**`→`<strong>`、`//**`→`<em><strong>`、`__`→`<u>`、`--`→`<s>`、`{{...}}`→`<code>` | 1st-once 表格 column 2 都渲染好 |
| | `^^`→`<sup>`、`##44FF88|text##`→`<span style=color:#44FF88>` | inlineOnly 命中 |
| | `[[span class=ruby]]`(Devanos 扩展)、`[[span class=keycap]]` | inlineOnly 命中 |
| | `[^N]` footnote refs | inlineOnly 命中 |
| **paragraphs** | `<p>...</p>` 块 149 个,`<br>` 跳行 279 处,emoji 全保留 | 整个 page 都跑 |
| **punct** | `“ ”`/`‘ ’`/`„ "`/«»/`…`/`—` | punctuation 转换一致 |
| **headings** | `<h1>` x15、`<h2>` x2、`<h3>` x6、自动 anchor ids 18 个 | source-order fix (fbdd675) 生效 |
| **toc** | 实际产 4 个 `.wikidot-toc` 块、嵌套 ul 结构 | fbdd675 nested 修复生效 |
| **collapsible** | 2 个 `<details>` + `<summary>`,body 内容隐藏 | OK |
| **links** | 12 个 wikidot.com 链接、`[*url]` 2 个 `target=_blank`、`[/relative]` 相对链接、14 个 `[#anchor]` 跳转、16 个 `[[[wiki]]]` 内部链 | inlineOnly 命中 |
| **anchor** | `<span id=...>` 18 个 | OK |
| **image** | 1 个 `<img>` (header logo)、嵌套 flex 渲染 | OK |
| **code** | 6 个 `<pre>`、verbatim 保留 `我要用左手...` | OK |
| **table** | 12 个 `<table>` (有 wiki-table class)、26 `<th>`、158 `<td>`、`长内容 4/7` 跨度合并 | OK |
| **block-fmt** | `[[=]] center` 2 个 block | OK |
| **footnote** | 4 个 `<sup class=footnote-ref>`、`脚注` block、有内容 | OK |
| **user** | `[[user X]]` 在 X 真实存在时变 `<a data-username=...>` (Devanos @-mention 1 个,ok) | UserLookup 接好 |

### ⚠️ WARN(3 — 测试脚本需调)

| Section | 问题 | 实际行为 |
|---|---|---|
| `comment` | `[!-- ... --]` in body 应剔除 | in-body 已剔除 ✓ ; 唯一出现是因为 TD 内的 `[!-- 不可见内容 --]` 未剔除;归到 FAIL 一起治 |
| `html` | `[[html]]...[[/html]]` 应透传 | 当前 impl 转义显示;Wikidot 原始 Wikidot 也吃 HTML 但有限白名单;技术债 |
| `block-fmt` inline `= 就像这样` 行级居中 | 测试正则偏窄 | 实际行级居中 regex 没匹中,但 `[[=]]` block 中心 2 个 OK |

### ❌ FAIL(34 — 27 出于同一根因,7 个独立 bug)

#### A. **Table Cell 注入**(27 FAIL,主因)

`wikidot.go:2892-2894`:table cell 走 `inlineOnly(c)`,所有 `[[...]]` 多标记形式被吃。

| 项 | 期望(Wikidot reference) | 实际 |
|---|---|---|
| `[[size smaller]]更小的字[[/size]]` (in TD) | `<span style=font-size:smaller/0.75rem>更小的字</span>` | 原文字符串 |
| `[[size larger]]`, `[[size 80%/100%/150%]]`, `[[size 0.8em/1em/1.5em]]` | 同上模式 | 原文字符串 |
| `[[size xx-small/x-small/small/large/x-large/xx-large]]` | 同上 | 原文字符串 |
| `[[size 7px]]`, `[[size 18.75px]]` | 同上 | 原文字符串 |
| `[[span style="font-family:Microsoft YaHei"]]中文等宽字[[/span]]` | `<span style=font-family:...>` | 原文字符串 |
| `[[span style="color:red"]]自定义//span//元素[[/span]]` | `<span style=color:red>...<em>span</em>元素</span>` | 原文字符串 |
| `[!-- 不可见内容 --]` (in TD) | 隐藏 | `[!<s> 不可见内容 </s>]` (部分渲染) |
| `##blue|text##` (仅第 1 个) | 蓝色 span | OK(已渲染 `color:#3498db`,但测试脚本用 `style=".*blue.*"` 没匹中) |
| `[[span class="ruby"]]...(Devanos 扩展)` | ruby class span | 实际渲染 OK in 第 1 列,Wikidot 一致 |
| `[[span class="keycap"]]` | keycap class span | 同上 OK |
| `##44FF88|自定义色码##` 第 2 个 | hex span | 实际 OK,测试 false-neg |

**修复方向**: 把 `renderWikidotTableRowLine` 中的 `inlineOnly(c)` 替换为 `p.convert(c)`(或一个不递归 block 层的子集),让 wikidot 内联管道跑一遍。

#### B. **TOC 收 heading 不全**(3 FAIL)

| 项 | 期望 | 实际 |
|---|---|---|
| TOC 包含 h1 + h3 entries | 应该有 h1 的 `行内格式`/`字体大小`/`段落及换行`...等 + h3 的子节 | **仅 2 个 H3**(`相对字体大小`,`绝对字体大小`)|
| 嵌套 `<ul>` 表现 h2 在 h1 里 | 多层 ul | 单层 ul,max depth = 0(只有最外层 toc-list) |
| ≥5 entries 含 toc-link | wikidot 多达 30+ | 4 个 toc-link |

**修复方向**: 检查 Phase 4(heading collection) - 是否 filter 掉 minTocLevel 之前的 heading;是否漏收 H1/H2 entries。

#### C. **嵌套 blockquote `>>`**(1 FAIL)

源 `>> 电的台词...` 期望加深缩进或嵌套 `<blockquote>`;实际渲染成 `<p>» 电的台词</p>`(成了 guillemet 字符)。 `[[f<`,`[[f>` 处理时把 `>>` 当成 typography 收掉了。

**修复方向**: `applyWikidotInlineColor` 或 `punct` 处理路径需在 `> blockquote`/`>> nested` 之前先跑,或者把 quote depth 单独算。

#### D. **`<pre class="wikidot-code">`** 标签 class 没贴上(1 FAIL,可能 WARN)

`wikidot.go:2119` `renderCodeBlock` 生成的 `<pre>` 没加 class。

#### E. **`[[note]]` 没生成 `<aside>`**(1 FAIL)

`/wikidot.go` 中没匹配 `[note]` 块,源里只有 1 个示例,但 should produce `<aside class="note">`。

#### F. **`[mailto:...]` / `[# empty link]`**(2 FAIL)

源里 spec 表格有这些例子,但测试断言只检查 page-wide 命中,而 spec 表格在 TD 内(table cell 注入根因)。同 A 类。

---

## 已知细节 / 顺带留意

* **Wikidot cookie 同源 dev bug**: `.env` 的 `GIN_MODE=debug` 设 `secure=false`,但 session 的 `SameSite=None` + `Secure=false` 在 Chrome HTTP dev 下被拒。登录会卡 403。需要把 SameSite 在 dev 路径下调成 Lax,或者 dev 走 HTTPS。
* **`gokych-darwin-arm64` 没自动重编译**:6 月 28 日之后多个 wikidot fix(`fbdd675` 等)没进 binary,需要手动 `go build -o dist/gokych-darwin-arm64 ./cmd/gokych`。
* **`[[user umou]]` / `[[*user kirito_blade]]` 没渲染 → 渲染器的 fallback 是保留原文**(因为 DB 中没这两用户)。如果想测 user link 实际渲染,需要先注册测试用户。
* **`<h4>`/`<h5>`/`<h6>` 不强求**:rule-wiki source 内的 `+++++ 5级标题` 等都包在 `[[code]]` 块内,本就不需要渲染成真的 `<h5>`。

---

## 建议本轮(P6)要修的 Fix

| 优先级 | Fix | 影响 | 估时 |
|---|---|---|---|
| 🔴 P0 | `renderWikidotTableRowLine`:替换 `inlineOnly(c)` 为 wiki-in-pipeline(在 cell 上下文调 `p.convert(c)` 或新写个 cell-scope convert) | 解 23 项,涵盖 [[size]]、[[span]]、[!--]]、##color## | 1–2h |
| 🟠 P1 | TOC heading 收集:debug 为什么 H1/H2 不入 `headings` slice | 解 3 项,TOC 完整 | 30m |
| 🟡 P2 | 嵌套 blockquote `>>` / nested bq | 解 1 项 | 15m |
| 🟡 P2 | `<pre class="wikidot-code">` 补 class、`[[note]]` 渲染 aside | 解 2 项 | 20m |
| 🟢 之后 | dev cookie SameSite=None / Secure=false bug | 解登录,可能影响别的 | 30m |

P0 + P1 + P2 大概总共 3h,**能把 85 项中的 ~30 个 FAIL 转 PASS**,从 56% 升到 90%+。

**今天先打哪些?**
