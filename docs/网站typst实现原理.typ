// 网站typst实现原理.typ
//
// GoKYCH 网站怎么集成 typst?从一篇 typst 文章的生命周期讲起:
//   用户编辑 → 发布(后端 fork typst CLI)→ 写 typst_cache →
//   读者访问(纯 SQL SELECT)→ 完。
//
// 这份文档面向开发者 / 运维 — 想了解 typst 集成的工作机制、或者
// 自己改 template.typ / 调缓存策略的同学。它本身也是 typst 源码,
// 走的是同一条 precompile 流水线。

#import "template.typ": template, hl, callout

#template[

= GoKYCH Typst 集成实现原理

== 设计目标

typst 是基于命令式的现代排版语言,质量上比 LaTeX 友好,速度上比
服务器端 Pandoc 流水线快。GoKYCH 集成它的目标:

- #hl["写"和"读"解耦]:编辑者写 typst 源码,读者看到排版好的
  HTML / PDF — 读者侧永远不应该感知 typst CLI 的存在
- #hl["发布"和"缓存"解耦]:发布是一次性成本,缓存是长久的收益;
  不能反过来让访问者承担发布成本
- #hl[失败早暴露]:typo / 语法错误必须在发布时就被抓住,而不是
  让读者在访问时看到「Typst 编译失败」

== 整体架构

```
  +---------------------+        fork typst CLI
  |  Go backend         | ──────────────────────────┐
  |  (cmd/gokych)       |                            ↓
  |                     |    +-----------------+   typst compile
  |  createArticle ──→ typst.CompileAndCache()   (HTML + PDF)
  |                     |    +-----------------+         ↓
  |                     |              ↓                  |
  |                     |    INSERT typst_cache           |
  |                     |              ↓                  |
  +---------------------+    ┌─────────────────┐          |
       ↑                    │  typst_cache    │ ←──────── ┘
       │  SELECT            │  (MySQL)        │
       │  (html / pdf)      └─────────────────┘
       │                              ↑
  +---------------------+             │
  |  Reader (browser)   | ────────────┘
  |  /api/articles/...  |
  |  /api/.../pdf       |
  +---------------------+
```

读路径永远不接触 typst 进程 — 这是性能保证的核心。

== 数据流:发布(写)

入口:`internal/api/articles.go:createArticle` / `updateArticle`。

- 类型是 `typst` 时,DB 写入 `articles` 表后调用
  `typst.CompileAndCache(a.ID, in.Content)`
- `CompileAndCache` 在 `internal/typst/typst.go`,内部调
  `compileBoth` 一次性跑出 HTML + PDF,然后用
  `INSERT ... ON DUPLICATE KEY UPDATE` 写到 `typst_cache` 表
- 失败处理:
  - create 模式:DELETE 文章 + 400 错误,slug 释放,用户重提
  - update 模式:UPDATE 还原 title / content + 400 错误,编辑器
    看到旧状态;`content.UpdateArticle` 自带的 `DELETE typst_cache`
    让 cache 处于空状态,与还原后的旧 content 一致

#callout(title: "为什么失败要回滚?")[
  如果保留新 content 但 cache 是空的,读者访问会看到「待编译」placeholder,
  跟 DB 里的「新内容」撕裂。回滚要么完全成功(新 content + 新 cache),
  要么完全没动(旧 content + 空 cache),UI 和 DB 永远一致。
]

== 数据流:访问(读)

入口:`internal/content/parsers/render.go:renderTypst` 和
`internal/api/pdf.go:getArticlePDF`。

- `renderTypst` 调 `typst.CompileHTMLCached(articleID)` — 纯读 SQL
- `getArticlePDF` 调 `typst.CompilePDFCached(articleID)` — 同样纯读
- cache 命中:直接返回 bytes
- cache miss:返回 error,UI 显示「本文档尚未编译完成」placeholder;
  PDF 端点返回 404 「PDF 尚未生成」

读路径不 fork typst CLI — 也不允许有 fallback 编译入口。改动
历史里有一版为了「保底」曾经在 cache miss 时调 `CompileHTML`,
导致每次冷启动都重新 fork typst;新版本严格只读,新文章没有
cache 就 placeholder,让管理员重新发布一次补上。

== 数据结构:typst_cache

```sql
CREATE TABLE typst_cache (
  id            INT AUTO_INCREMENT PRIMARY KEY,
  article_id    INT NOT NULL UNIQUE,
  html_content  LONGTEXT NOT NULL,
  pdf_content   LONGBLOB  NOT NULL,
  dependencies  TEXT DEFAULT NULL,
  compiled_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
);
```

- `article_id` 唯一 — 一个 typst 文章对应一行 cache
- `ON DELETE CASCADE` — 文章删除时 cache 自动清,无需额外清理
- `html_content` 是 typst CLI `--format html --features html` 输出的
  body 段(`extractBody` 剥掉 `<!DOCTYPE>` / `<html>` / `<head>` /
  `<body>` 标签,只保留 body 内容方便嵌入页面模板)
- `pdf_content` 是 typst 默认输出(typst 本身就是 PDF-first)
- `compiled_at` 记录最后一次编译时间,DEBUG 用

== 关键代码位置

- `internal/typst/typst.go` — typst CLI 封装、缓存、并发控制
- `internal/typst/assets/template.typ` — 嵌入的默认页面模板
- `internal/content/parsers/render.go` — 调用 `CompileHTMLCached` 渲染
- `internal/api/pdf.go` — PDF 下载端点
- `internal/api/articles.go` — createArticle / updateArticle 调用
  `CompileAndCache`
- `internal/content/articles.go:UpdateArticle` — 自带 `DELETE typst_cache`
  作为兜底 invalidate(在 precompile 失败时让 cache 处于空状态)

== 并发与资源保护

typst CLI 是个独立进程,可能慢、可能 hang、可能耗内存。
`internal/typst/typst.go` 顶部的常量:

```go
compileTimeout = 30 * time.Second
maxConcurrent  = 4
compileSem     = make(chan struct{}, maxConcurrent)
```

- `context.WithTimeout(30s)` — 死循环文档最多卡 30s 然后被 kill
- `compileSem` 4 并发上限 — 防 fork-bomb、防磁盘耗尽

`envWhitelist()` 只透传 8 个白名单 env var(`PATH` / `HOME` /
`LANG` / `TMPDIR` 等),不把 `SESSION_SECRET` / `DB_PASSWORD` 等
父进程 secret 透给子进程。

== workspace 与素材

- workspace 目录:`<DATA_DIR>/typst/`,由 `main.go` 调
  `typst.SetWorkspaceDir` 在启动时设定
- `template.typ` 由 `embed.FS` 嵌入到二进制,首次启动物化到
  workspace;已存在则不覆盖(让用户改本地版本)
- typst CLI 在 workspace 内执行,所以 `#import "template.typ"`
  能找到文件
- 临时输入 / 输出文件(`.input_<n>.typ` / `.output_<n>.html` /
  `.output_<n>.pdf`)用 `UnixNano + PID` 命名,进程退出时 defer
  清理;`cleanupLeakedInputs` 在启动时扫掉上次崩溃留下的临时文件

== 错误处理路径

#table(
  columns: (1fr, 1fr),
  align: (left, left),
  [场景], [表现],
  [typst CLI 没装], [启动时 `WARN` 日志;createArticle 报 400 「Typst 编译器未安装」],
  [源文件语法错], [发布时 400,带 CLI stderr 输出],
  [发布后 typst 升级 / 字体变更], [cache 不会自动失效,需手动重新发布],
  [typst 子进程 hang], [30s timeout 后 `cmd.CombinedOutput` 返回 error],
  [同一文章并发发布], [`ON DUPLICATE KEY UPDATE` last-write-wins],
  [cache miss(旧文章)], [HTML 显示 placeholder;PDF 返回 404],
)

== 部署注意

- typst CLI 必须装(debian: `apt install typst`,macOS: `brew install typst`,
  prebuilt: GitHub releases)
- 中文字体:`template.typ` 用 `Noto Serif CJK SC` 等,系统得装对应
  font 包(debian: `fonts-noto-cjk`,macOS 自带)
- workspace 目录磁盘空间:PDF 缓存随文章增长,长文单篇可能几 MB;
  100 篇 1MB 文章 ≈ 100MB typst_cache,定期 audit
- 后端内存:LONGBLOB 直接存 `pdf_content`,MySQL buffer pool 会
  缓存热点文章的 PDF;`innodb_log_file_size` 建议 ≥ 256MB
- 不要把 workspace 目录挂在 NFS / 慢盘上 — typst CLI 是密集 IO

== 监控指标

看 slog 日志里的:

- `WARN typst pdf compile failed` — PDF 部分失败但 HTML 成功
  (precompile 早期版本的容错行为,新版本会整体失败)
- `ERROR typst cache lookup` — DB 读 cache 失败
- `INFO typst render: cache miss` — 旧文章无 cache,提示用户
  重新发布
- `ERROR getArticlePDF: cache lookup` — PDF 端点 cache miss 路径

= 总结

Typst 集成是 GoKYCH 内容引擎的一块关键拼图。设计上的关键决策:

- #hl[写一次,读 N 次] — 用 publish-time 编译换 reader 性能
- #hl[失败 fail-fast,绝不半完成] — 任何编译错都拒绝发布,UI 和 DB
  永远一致
- #hl[读路径纯 SQL] — 读者体验不依赖 typst 进程是否健康

想参与修改的话,主要代码集中在 `internal/typst/` 目录,改动后
跑 `go test ./internal/typst` 验证 E2E 编译流程。

]
