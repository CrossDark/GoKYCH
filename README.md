# GoKYCH

跨越晨昏的 Go 后端实现 — WikiDot 风格的多类型内容发布平台。

Go 1.26+ · Gin · MySQL 8 · Next.js 15 前端在 `web/`

---

## 项目简介

GoKYCH 是一个个人 wiki/blog 平台，参考 PyKYCH（同作者的 Python 前身）实现。后端用 Go 写，
前端用 Next.js 15（App Router + TypeScript）。数据落在 MySQL，静态资源走文件系统。

支持 5 种文章类型：

| 类型     | 路径前缀     | 渲染器                        |
|----------|-------------|-------------------------------|
| Markdown | `/md/`      | goldmark（自托管）             |
| Wikidot  | `/wikidot/` | 自研解析器                    |
| BBCode   | `/bbcode/`  | 自研解析器                    |
| HTML     | `/html/`    | 透传 + DOMPurify 兜底         |
| Typst    | `/typst/`   | 调 typst CLI → HTML + PDF     |

每篇文章有：

- 标签（多对多）
- 评分（-1 到 1 的浮点）
- 行评论（≤20 字符，电脑端侧边浮泡，移动端底部）
- 全文评论（支持 Markdown）
- 缩略图、推荐位、置顶

### Typst 独有能力

- **PDF 导出**：每篇 typst 文章都可以通过 `/typst/{slug}/pdf` 下载 PDF
- **跨文章导入**：`#import "@slug"` / `#include "@slug"` 引用其他 typst 文章，依赖自动解析、自动级联重编译
- **引用上传文件**：直接使用后台文件管理器中的路径，如 `#image("/uploads/xxx.png")`、`#import "/uploads/lib.typ"`、`#bibliography("/uploads/refs.bib")`，系统自动处理路径
- **内置模板**：`template.typ` 提供跨平台中文字体链（macOS/Linux/Windows）、标题/代码/链接样式
- **缓存预热**：发布时编译 HTML+PDF，读者访问零编译开销

---

## 快速开始

### 本地开发

```bash
# 1. 准备 MySQL 8.0+ 与一个空库
mysql -u root -p -e "CREATE DATABASE gokych DEFAULT CHARSET utf8mb4;"

# 2. 复制环境模板
cp .env.example .env   # 改 DB_USER / DB_PASSWORD / ADMIN_PASSWORD

# 3. 启动后端（自动建表 + seed 默认 owner）
go run ./cmd/gokych

# 4. 启动前端（另一终端）
cd web
npm install
npm run dev
# 打开 http://localhost:3000
# 后台 http://localhost:3000/admin  →  admin / admin123
```

> **前置依赖**：typst 文章需要安装 typst CLI（`brew install typst` 或从 [typst.app](https://typst.app) 下载）。不安装也能运行，只是 typst 类型文章会显示"编译中"占位符。

### 编译 + 二进制部署

```bash
go build -o ./gokych ./cmd/gokych
./gokych
```

### Docker

```bash
docker build -t gokych .
docker run -d --name gokych \
  -p 8000:8000 \
  -e DB_HOST=host.docker.internal \
  -e DB_PASSWORD=yourpass \
  -e SESSION_SECRET=your-strong-secret \
  -v $(pwd)/data:/app/data \
  gokych
```

### 生产部署（EdgeOne + VM）

推荐架构：Next.js 前端部署在 EdgeOne Pages / Cloudflare Pages，Go 后端运行在 VPS（Nginx 反向代理）。详细部署步骤见 [docs/deployment.md](docs/deployment.md)。

关键环境变量（前端）：
- `NEXT_PUBLIC_API_BASE_URL=https://api.example.com` — 指向你的后端API地址

---

## 配置

### 环境变量

| 变量                  | 默认值                     | 说明                                              |
|-----------------------|---------------------------|---------------------------------------------------|
| `DB_HOST`             | `localhost`               | MySQL host                                        |
| `DB_PORT`             | `3306`                    | MySQL port                                        |
| `DB_USER`             | `gokych`                  | MySQL user                                        |
| `DB_PASSWORD`         | `gokych`                  | MySQL password                                    |
| `DB_NAME`             | `gokych`                  | MySQL database                                    |
| `DB_CHARSET`          | `utf8mb4`                 |                                                   |
| `DB_POOL_MIN`         | `5`                       | 连接池最小连接数                                   |
| `DB_POOL_MAX`         | `25`                      | 连接池最大连接数                                   |
| `DB_POOL_RECYCLE`     | `3600`                    | 连接回收秒数                                       |
| `APP_PORT`            | `8000`                    | HTTP 监听端口                                     |
| `GIN_MODE`            | `debug`                   | `debug` / `release` / `test`                      |
| `SESSION_SECRET`      | `change-me-...`           | ⚠️ release 模式下必须改，建议 32+ 字节随机串       |
| `ADMIN_USERNAME`      | `admin`                   | 首次启动 seed 的 owner 用户名                      |
| `ADMIN_PASSWORD`      | `admin123`                | ⚠️ 首次启动后立刻改                                |
| `DATA_DIR`            | `data`                    | 数据/上传/主题/插件/typst工作区目录                |
| `PUBLIC_URL`          | （空）                    | 后端对外的公开 URL，用于跨域场景拼接静态资源绝对路径 |
| `CORS_ALLOWED_ORIGINS`| （空）                    | 逗号分隔的允许跨域来源（如 `https://eo.example.com`）|
| `SESSION_COOKIE_DOMAIN`| （空）                   | Cookie 域名，跨子域部署时设为 `.example.com`       |
| `TRUSTED_PROXIES`     | （空）                    | 逗号分隔的代理 CIDR/IP，空=不信任任何代理          |
| `APP_DOMAIN`          | （空）                    | Passkey 依赖域名。空则禁用 Passkey                |

### YAML 配置

- `$DATA_DIR/settings/db.yaml` — 覆盖 MySQL 设置
- `$DATA_DIR/settings/settings.yml` — 站点元数据（标题、ICP备案、主题、首页配置、favicon），由后台 `/admin/settings` 维护

---

## 目录结构

```
.
├── cmd/gokych/                # 入口 main.go
├── internal/
│   ├── api/                   # HTTP handler + 路由 + 中间件
│   │   ├── auth.go            #   登录/注册/登出/CSRF
│   │   ├── admin.go           #   后台 CRUD（用户/通知/设置/API Key/Passkey/更新/文件/标签/首页/资料）
│   │   ├── articles.go        #   公共文章 API（含 typst 编译触发）
│   │   ├── comments.go        #   评论/行评论
│   │   ├── ratings.go         #   评分
│   │   ├── files.go           #   文件上传/管理 + 静态资源URL重写
│   │   ├── site.go            #   站点公开配置
│   │   ├── pdf.go             #   typst PDF 输出端点
│   │   ├── middleware.go      #   auth/csrf/session/gzip/nonce
│   │   ├── requestid.go       #   X-Request-ID
│   │   ├── security.go        #   CSP/HSTS/X-Frame-Options
│   │   └── dberr.go           #   MySQL 1062 判重
│   ├── auth/                  # 认证子系统
│   │   ├── session/           #   会话管理
│   │   ├── password/          #   bcrypt 密码哈希
│   │   ├── ratelimit/         #   登录限流（失败锁定）
│   │   ├── passkey/           #   WebAuthn/Passkey 无密码登录
│   │   ├── apikey/            #   API Key 鉴权（X-API-Key 头）
│   │   └── user/              #   用户 CRUD
│   ├── content/               # 文章数据层
│   │   ├── parsers/           #   markdown / wikidot / bbcode / html 解析器
│   │   ├── articles.go        #   文章 CRUD + typst 缓存集成
│   │   ├── render_cache.go    #   rendered_html 缓存预热/失效
│   │   ├── comments.go        #   评论
│   │   ├── ratings.go         #   评分
│   │   └── tags.go            #   标签
│   ├── core/
│   │   ├── db/                #   数据库连接池
│   │   ├── schema/            #   自动建表/迁移
│   │   ├── settings/          #   settings.yml 读写
│   │   ├── themes/            #   主题加载
│   │   ├── metrics/           #   Prometheus 指标
│   │   └── logging/           #   slog 配置
│   └── typst/                 # typst CLI 包装器
│       ├── typst.go           #   编译/缓存/资源链接/路径重写
│       ├── worker.go          #   异步编译 worker + 依赖级联
│       ├── resolve.go         #   @slug 跨文章导入解析
│       └── assets/
│           └── template.typ   #   默认中文排版模板（物化到工作区，用户可修改）
├── examples/
│   └── wikidot-demo-render/   # Wikidot 渲染示例
├── data/                      # 运行时数据（不进git）：uploads/avatars/plugins/themes/typst
├── web/                       # Next.js 15 前端（App Router）
│   ├── app/                   #   路由页面
│   ├── components/            #   React 组件
│   ├── lib/                   #   API客户端 + 工具函数
│   └── styles/                #   全局CSS
├── scripts/                   # 部署/构建脚本
├── docs/                      # 部署/开发文档
│   ├── deployment.md          #   生产部署指南（EdgeOne/Nginx/Caddy）
│   ├── development.md         #   开发指南
│   └── typst写作指南.typ       #   Typst写作指南（可发布为网站文章）
└── wiki/                      # 项目Wiki
    ├── Typst写作指南.md        #   Typst写作指南（Markdown版）
    ├── 部署指南.md
    ├── 开发指南.md
    └── ...
```

---

## API 一览

### 公开读（无需认证）

| 方法 | 路径                                            | 说明                       |
|------|------------------------------------------------|----------------------------|
| GET  | `/api/site`                                    | 站点元数据 + 子站点链接    |
| GET  | `/api/home`                                    | 首页聚合（推荐+最近+通知） |
| GET  | `/api/articles?type=md&page=1`                 | 文章列表                   |
| GET  | `/api/articles/{type}/{slug}`                  | 文章详情                   |
| GET  | `/api/articles/{type}/{slug}/pdf`              | Typst PDF 下载             |
| GET  | `/api/search?q=foo`                            | 全文搜索（FULLTEXT索引）   |
| GET  | `/api/labels` / `/api/labels/{tag}`            | 标签列表/标签下文章        |
| GET  | `/api/articles/{type}/{slug}/comments`         | 评论列表                   |
| GET  | `/api/articles/{type}/{slug}/line-comments`    | 行评论                     |
| GET  | `/api/articles/{type}/{slug}/rating`           | 评分摘要                   |
| GET  | `/api/notifications`                           | 通知列表（需登录）         |
| GET  | `/uploads/{filename}`                          | 上传的静态文件             |
| GET  | `/avatars/{filename}`                          | 用户头像                   |

### 认证

| 方法 | 路径                       | 说明                          |
|------|---------------------------|-------------------------------|
| GET  | `/api/auth/me`            | 当前用户（或 null）           |
| GET  | `/api/auth/csrf`          | CSRF token + 数学验证码        |
| POST | `/api/auth/login`         | 用户名+密码+验证码             |
| POST | `/api/auth/logout`        | 退出                          |
| POST | `/api/auth/passkey/...`   | Passkey 注册/登录/管理         |

### 写操作（需 CSRF + 登录）

| 方法   | 路径                                                  | 说明               |
|--------|------------------------------------------------------|--------------------|
| POST   | `/api/articles/{type}/{slug}/comments`               | 发表评论           |
| POST   | `/api/articles/{type}/{slug}/line-comments`          | 发表行评论         |
| POST   | `/api/articles/{type}/{slug}/rating`                 | 评分               |
| DELETE | `/api/articles/{type}/{slug}/rating`                 | 撤销评分           |
| POST   | `/api/articles?type=md`                              | 创建文章（管理员） |
| PUT    | `/api/articles/{type}/{slug}`                        | 更新文章（管理员） |
| DELETE | `/api/articles/{type}/{slug}`                        | 删除文章（管理员） |
| POST   | `/api/admin/upload`                                  | 上传文件（管理员） |
| /api/admin/*                                              | 后台管理 API       |

所有响应都是 JSON，错误格式 `{"error": "..."}`。GET 成功直接返回数据对象/数组；写成功返回 `{"status":"ok", ...}`。

匿名文章页响应带 `Cache-Control: public, max-age=300, stale-while-revalidate=3600`；
登录用户页面带 `private, max-age=30`，避免缓存个性化内容。

---

## 性能优化

- **errgroup 并行查询**：文章详情页对 rating/comments/lineComments/compileStatus 等独立DB查询并行执行
- **Typst HTML/PDF 并行编译**：使用 errgroup 同时编译两种格式，墙钟时间减半
- **KaTeX/Mermaid 按需加载**：只有文章包含数学公式/图表时才动态导入，首屏JS减少约800KB
- **全文索引**：`articles.content` + `articles.title` 建有 MySQL FULLTEXT 索引
- **Gzip 压缩**：所有文本响应（JSON/HTML/JS/CSS）自动 gzip
- **连接池调优**：默认 Pool.Min=5, Pool.Max=25
- **静态资源长缓存**：`/uploads/*` 和 `/avatars/*` 带 1 年 Cache-Control（文件名基于hash/UUID，更新会生成新URL）

---

## 安全

- 密码 bcrypt 12 轮，最多 72 字节
- 会话 cookie secure/httpOnly（release 模式），支持 SameSite
- CSRF token 32 字节 crypto/rand，双重提交 cookie + header
- CSP nonce 机制（每个请求唯一 nonce，inline script 必须带 nonce 属性）
- 登录限流：每 (user,ip) 5/min 失败、20/min 总失败、10 次失败锁 15min
- TrustedProxies 严格控制 X-Forwarded-For 解析
- Content-Security-Policy / X-Frame-Options / X-Content-Type-Options / HSTS 统一中间件
- BBCode/Wikidot URL 与 CSS 注入过滤，DOMPurify 消毒 HTML
- 文件上传 MIME 白名单（图片/PDF等）+ 大小上限 10MB，文件名使用UUID避免路径遍历
- API Key 权限：仅站长可用，写操作跳过CSRF

---

## 开发

```bash
# 静态检查
go vet ./...
go test ./... -count=1 -short
gofmt -l ./internal/ ./cmd/
golangci-lint run

# 前端
cd web
npm run lint
npm run typecheck
npm run build
```

---

## 扩展点

- **主题** — `data/themes/<name>/theme.yaml` + `theme.css`，后台选择
- **Typst 模板** — 直接编辑服务器上 `data/typst/template.typ`（首次启动后不被覆盖），添加自定义函数/样式
- **Typst 跨文章引用** — 使用 `#import "@slug"` 在文章之间共享代码/宏/模板
- **Typst 引用上传文件** — 直接使用后台上传的路径（`/uploads/xxx.png`），无需配置
- **API Key** — 后台创建 API Key，通过 `X-API-Key` 请求头鉴权，适合脚本/CI集成
- **Passkey** — 支持 WebAuthn 无密码登录（需要配置 `APP_DOMAIN` 和 HTTPS）
- **插件** — `data/plugins/` 目录可扩展功能

---

## License

MIT
