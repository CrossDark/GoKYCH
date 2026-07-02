// 网站typst实现原理.typ
//
// GoKYCH 网站怎么集成 typst?从一篇 typst 文章的生命周期讲起:
//   用户编辑 → 发布(入队异步编译)→ Worker 队列编译 → 写 typst_cache + rendered_html →
//   读者访问(读缓存,零编译开销)→ 完。
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

- #hl["写"和"读"完全解耦]:编辑者写 typst 源码,读者看到排版好的
  HTML / PDF — HTTP 请求永远不阻塞在 typst CLI 进程上
- #hl["发布"即入队,不等待编译]:发布请求立即返回,编译在后台 Worker 完成
- #hl["统一渲染缓存"]:所有文章类型(typst/md/wikidot/bbcode/html)
  都有 rendered_html 缓存,支持 CDN Edge 缓存
- #hl["依赖自动追踪"]:@import 跨文章依赖、Wikidot [[include]] 依赖
  自动解析,更新时级联重编译
- #hl["失败早暴露,有重试"]:编译错误保留在队列中,最多重试 3 次

== 整体架构

```
  +---------------------+        入队 typst_compile_queue
  |  Go backend         | ──────────────────────────┐
  |  (HTTP handler)     |                            ↓
  |                     |    +-----------------+    Worker goroutine
  |  createArticle ──→ EnqueueCompile()  ───→  SELECT ... FOR UPDATE SKIP LOCKED
  |  updateArticle      |    +-----------------+         ↓
  |  deleteArticle      |              ↓                  |
  +---------------------+    compileBothCtx()           typst compile
       ↑                    (HTML + PDF 并行)             ↓
       │  SELECT rendered_html  ↓                         |
       │  / pdf_content     INSERT typst_cache            |
       │                     + article_deps              |
       │                     + rendered_html(articles表)   |
       │                              ↓                  |
       │                    +-----------------+          |
  +---------------------+   │  MySQL Cache    │ ←─────── ┘
  |  Reader (browser)   |   │                 |
  |  /api/articles/...  |   │ • typst_cache   |  (html + pdf)
  |  /api/.../pdf       |   │ • article_deps  |  (依赖图)
  +---------------------+   │ • articles.     |
                             │   rendered_html | (统一HTML缓存)
                             +-----------------+
```

HTTP 请求路径(读/写)*永远不*阻塞在 typst CLI — 这是性能和稳定性的核心保证。

== 核心数据结构

=== typst_cache 表

存储编译产物(HTML+PDF),是读路径的唯一数据源:

```sql
CREATE TABLE typst_cache (
  id            INT AUTO_INCREMENT PRIMARY KEY,
  article_id    INT NOT NULL UNIQUE,
  html_content  LONGTEXT NOT NULL,
  pdf_content   LONGBLOB  NOT NULL,
  compiled_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
);
```

- `html_content` 是 typst CLI `--format html` 输出,后处理过
  (extractBody 提取 body 内容、静态资源路径重写)
- `pdf_content` 是 typst 默认 PDF 输出
- `ON DELETE CASCADE` — 文章删除时 cache 自动清理

=== article_deps 表

追踪跨文章依赖图(typst @import、Wikidot [[include]]):

```sql
CREATE TABLE article_deps (
  id           INT AUTO_INCREMENT PRIMARY KEY,
  article_id   INT NOT NULL,
  depends_on   INT NOT NULL,
  dep_type     VARCHAR(20) NOT NULL,   -- 'typst_import' / 'wikidot_include'
  UNIQUE KEY (article_id, depends_on),
  FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
  FOREIGN KEY (depends_on) REFERENCES articles(id) ON DELETE CASCADE
);
```

当依赖文章更新时,BFS 遍历反向依赖图,级联失效所有下游缓存并重新入队编译。

=== typst_compile_queue 表

异步编译队列,Worker 从此表领取任务:

```sql
CREATE TABLE typst_compile_queue (
  article_id    INT PRIMARY KEY,
  status        ENUM('pending','compiling','completed','failed') NOT NULL,
  error_message TEXT,
  attempts      INT NOT NULL DEFAULT 0,
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  compiled_at   DATETIME NULL,
  FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
);
```

=== articles.rendered_html 列

*所有*文章类型共享的统一 HTML 缓存列:

```sql
ALTER TABLE articles ADD COLUMN rendered_html LONGTEXT NULL;
```

- typst:编译后 HTML 后处理后同步到这里
- md/wikidot/bbcode/html:发布时直接渲染并写入
- 服务启动时 `WarmCache` 预热最近 50 篇文章的缓存
- 文章详情页 SSR 带 `revalidate=300`,支持 CDN 缓存

== 数据流:发布(写路径)

入口:`internal/api/articles.go:createArticle` / `updateArticle` /
`deleteArticle`。

=== 创建/更新 typst 文章

1. DB 写入 `articles` 表,content 存原始 typst 源码
2. 调用 `worker.EnqueueCompileCtx(ctx, articleID)` — 仅入队,
   *不*等待编译
3. HTTP 请求立即返回,编辑器显示"编译中"状态
4. Worker 后台 goroutine 轮询队列领取任务

=== Worker 编译流程

1. 使用 `SELECT ... FOR UPDATE SKIP LOCKED` 原子领取一个 pending 任务,
   标记为 `compiling`
2. 调用 `compileBothCtx(ctx, articleID, content)` — errgroup 并行编译
   HTML 和 PDF(各占一个 compileSem 槽位)
3. 编译前预处理:
   - 调用 `rewriteAssetPaths()` 将 `/uploads/xxx`、`/avatars/xxx`
     重写为 workspace 相对路径
   - 调用 `resolveImports()` 解析 `@slug` 跨文章引用,将 `@other-article`
     替换为实际文件名 `article_<id>.typ`,并写入依赖到 article_deps
   - 确保 `uploads/`、`avatars/` 符号链接存在于 workspace
4. 执行 typst CLI(带 60s context timeout)
5. 后处理 HTML:
   - `extractBody()` 剥离 html/head/body 标签
   - `rewriteStaticAssetURLs()` 重写静态资源路径为绝对 URL(当 PublicURL 配置时)
6. 事务更新:
   - UPSERT `typst_cache` 表(html_content + pdf_content)
   - 更新 `articles.rendered_html`
   - 刷新 `article_deps` 依赖(旧依赖删除,新依赖写入)
   - 标记队列状态为 `completed`,记录 compiled_at
7. 编译成功后,调用 `EnqueueDependentsCtx()` 级联入队所有依赖本文的文章
8. 编译失败:
   - 记录 `error_message`,递增 `attempts`
   - attempts < 3 时重置为 `pending` 等待重试
   - attempts ≥ 3 标记为 `failed`,保留错误信息供管理员查看

=== 删除文章

1. 删除 articles 表记录(ON DELETE CASCADE 自动清理 typst_cache、
   article_deps、compile_queue)
2. 调用 `InvalidateDependentsCtx()` 失效并重新编译依赖本文的文章

== 数据流:访问(读路径)

入口:`internal/content/parsers/render.go:renderTypst` 和
`internal/api/pdf.go:getArticlePDF`。

=== HTML 渲染

- 优先读 `articles.rendered_html` — 纯 SQL SELECT,零进程开销
- 如果为空(旧文章、编译中),检查 typst_compile_queue 状态:
  - pending/compiling → 显示"编译中"占位符
  - failed → 显示"编译失败"+ 错误信息(仅管理员可见)
  - 无队列记录 → 显示待编译提示,管理员可触发重编译

=== PDF 下载

- 读 `typst_cache.pdf_content`
- 存在则直接返回 bytes,Content-Type: application/pdf
- 不存在返回 404

=== 缓存策略

- 匿名用户:Cache-Control: public, max-age=300, stale-while-revalidate=3600
  (支持 EdgeOne/Cloudflare CDN 边缘缓存)
- 登录用户:Cache-Control: private, max-age=30(避免缓存个性化内容)

== 关键代码位置

- `internal/typst/typst.go` — typst CLI 封装、资源链接、路径重写、
  compileBothCtx 并行编译
- `internal/typst/worker.go` — 异步 Worker、队列领取、重试、状态查询
- `internal/typst/worker_state.go` — Worker 结构体、AfterCompileFunc
- `internal/typst/resolve.go` — @slug 跨文章导入解析、依赖图构建
- `internal/typst/assets/template.typ` — 嵌入的默认页面模板
- `internal/content/render_cache.go` — rendered_html 缓存预热/失效/
  WarmCache 启动预热
- `internal/content/articles.go` — 文章 CRUD + 缓存失效集成
- `internal/api/pdf.go` — PDF 下载端点
- `internal/api/admin.go` — 管理员重编译触发端点
- `cmd/gokych/main.go` — SetWorkspaceDir/SetAssetsDirs 初始化、Worker 启动

== 并发与资源保护

typst CLI 是独立进程,可能慢、可能 hang、可能耗内存。

=== 进程级并发控制

```go
compileTimeout = 60 * time.Second   // 单篇编译超时(恶意/死循环文档保护)
maxConcurrent  = 4                 // 同时最多 4 个 typst 进程
compileSem     = make(chan struct{}, maxConcurrent)
```

- `exec.CommandContext` 带 60s 超时,超时 kill 进程
- `compileSem` 信号量限制并发,防 fork-bomb、防内存耗尽
- HTML 和 PDF 并行编译各占一个槽位(单篇文章最多占 2 槽)

=== 队列级原子性

- `SELECT ... FOR UPDATE SKIP LOCKED` 确保多实例部署时任务不重复领取
- 任务状态机:pending → compiling → completed/failed
- 最多 3 次重试,失败保留错误信息

=== 环境变量安全

`envWhitelist()` 只透传白名单环境变量(`PATH`/`HOME`/`LANG`/`TMPDIR`等),
不把 `SESSION_SECRET`/`DB_PASSWORD` 等密钥透给子进程。

== workspace 与素材文件

- workspace 目录:`<DATA_DIR>/typst/`,由 `main.go` 调
  `SetWorkspaceDir` 在启动时设定
- 符号链接(启动时创建、编译前校验):
  - `workspace/uploads/` → `<DATA_DIR>/uploads/`(用户上传文件)
  - `workspace/avatars/` → `<DATA_DIR>/avatars/`(用户头像)
- 路径重写(`rewriteAssetPaths`):
  - 正则 `(^|[\s(,:=])("|')/(uploads/|avatars/)` 匹配源码中的绝对路径
  - 重写为相对路径 `$1$2$3`,使 typst 能通过符号链接找到文件
  - 支持所有文件引用语法:`#image("/uploads/...")`、
    `#import "/uploads/lib.typ"`、`#bibliography("/uploads/refs.bib")`、
    `#read("/uploads/data.csv")` 等
- `template.typ` 由 `embed.FS` 嵌入二进制,首次启动物化到
  workspace;已存在则不覆盖(允许用户自定义本地版本)
- 临时文件(`.input_<n>.typ`/`.output_<n>.html`/`.output_<n>.pdf`)
  用 `UnixNano + PID` 命名,defer 清理;启动时 `cleanupLeakedInputs`
  扫掉上次崩溃留下的临时文件
- 跨文章引用解析:
  - `@slug` 语法引用其他 typst 文章(如 `#import "@helpers"`)
  - resolve 阶段将被引用文章写入 workspace 为 `article_<id>.typ`
  - 循环依赖检测(A→B→A)返回明确错误

== 错误处理路径

#table(
  columns: (1fr, 2fr),
  align: (left, left),
  [场景], [表现],
  [typst CLI 没装], [启动时 WARN 日志;入队时直接标记 failed,提示安装 typst],
  [源文件语法错], [队列标记 failed,error_message 包含 CLI stderr;管理员可在后台查看并重试],
  [编译超时(>60s)], [context 超时 kill 进程,标记 failed,计入重试次数],
  [@import 循环依赖], [resolve 阶段检测到,返回明确错误"circular dependency detected"],
  [上传文件不存在], [typst CLI 报错"file not found",标记 failed],
  [发布后 typst 升级/字体变更], [cache 不自动失效;管理员工具箱可手动触发全站重编译],
  [同一文章并发更新], [队列 UPSERT 重置为 pending,最后一次更新触发重编译;Worker 单线程领取],
  [依赖文章更新], [BFS 遍历反向依赖图,级联入队重编译;先失效下游缓存(显示"编译中")],
  [Worker 崩溃重启], [staleCompileTimeout=5min 后,未完成任务重置为 pending 重新领取],
)

== 部署注意

- typst CLI 必须安装(debian: `apt install typst`,macOS:
  `brew install typst`,prebuilt: GitHub releases)
- *中文字体*:`template.typ` 顶部声明跨平台 fallback 链
  (cjk-serif / cjk-sans / mono),覆盖 Noto Serif CJK SC /
  Source Han Serif SC / Songti SC / STSong / SimSun / PingFang SC /
  Hiragino Sans GB / Sarasa Gothic SC / WenQuanYi Zen Hei 等。
  生产 VM 必须装:`apt install fonts-noto-cjk fonts-wqy-microhei`。
  *不装的话 PDF 路径会显示 missing glyph 方块*,HTML 不受影响
  (浏览器有系统字体 fallback)
- 代码块 CJK 兜底:`template.typ` 的 mono 字体链里把
  `Sarasa Mono SC` 放在靠后位置,解决"中文注释 + ASCII 标记"混排
  的方块字问题
- workspace 目录磁盘空间:PDF 缓存随文章增长,长文单篇可能几 MB;
  uploads 目录也要预留空间(用户上传图片/PDF/数据文件)
- 后端内存:LONGBLOB 存 pdf_content,MySQL buffer pool 缓存热点文章;
  `innodb_log_file_size` 建议 ≥ 256MB
- 不要把 workspace/uploads 目录挂在 NFS / 慢盘上 — typst CLI 是密集 IO
- Docker 容器:必须以非 root 用户运行(gokych:1001),uploads/avatars/typst
  目录需要写权限

== 监控指标

看 slog 日志里的:

- `INFO typst: enqueued for async compile` — 文章入队编译
- `INFO typst: compile succeeded` — 编译成功,耗时、文章 ID
- `ERROR typst: compile failed (attempt N/3)` — 编译失败,含错误摘要
- `WARN typst: pdf compile failed` — PDF 单独失败(HTML 成功)
- `INFO typst: enqueuing N dependents for recompilation` — 级联重编译触发
- `ERROR typst: worker claim task` — Worker 领取任务出错
- `INFO typst: reset stale compiling task` — 崩溃恢复,重置僵死任务
- `INFO cache: warmup complete` — 启动预热完成,N 篇文章已渲染

= 总结

Typst 集成是 GoKYCH 内容引擎的一块关键拼图。当前版本的关键设计决策:

- #hl[完全异步]:HTTP 请求零阻塞,Worker 后台消费队列
- #hl[统一缓存]:所有文章类型共享 rendered_html,支持 CDN 缓存
- #hl[依赖追踪]:BFS 级联失效,跨文章引用自动重编译
- #hl[用户素材无缝集成]:符号链接 + 路径重写,直接引用上传文件
- #hl[容错重试]:最多 3 次重试,崩溃自动恢复,错误信息保留

想参与修改的话,主要代码集中在 `internal/typst/` 目录,改动后
跑 `go test ./internal/typst -v` 验证 E2E 编译流程和依赖解析。

]
