# GoKYCH

跨越晨昏的 Go 后端实现 — WikiDot 风格的多类型内容发布平台。

Go 1.26+ · Gin · MySQL 8 · Next.js 15 前端在 `web/`

---

## 项目简介

GoKYCH 是一个个人 wiki/blog 平台，参考 [PyKYCH](https://...) 实现。后端用 Go 写，
前端用 Next.js 15。数据落在 MySQL，静态资源走文件系统。

支持 5 种文章类型：

| 类型    | 路径前缀    | 渲染器                 |
|---------|------------|------------------------|
| Markdown | `/md/`     | goldmark（自托管）      |
| Wikidot  | `/wikidot/`| 自研解析器              |
| BBCode   | `/bbcode/` | 自研解析器              |
| HTML     | `/html/`   | 透传 + DOMPurify 兜底  |
| Typst    | `/typst/`  | 调 typst CLI → HTML    |

每篇文章有：

- 标签（多对多）
- 评分（-1 到 1 的浮点）
- 行评论（≤20 字符）
- 全文评论
- 缩略图、推荐位

---

## 快速开始

### 本地开发

```bash
# 1. 准备 MySQL 8.0+ 与一个空库
mysql -u root -p -e "CREATE DATABASE gokych DEFAULT CHARSET utf8mb4;"

# 2. 复制环境模板
cp .env .env.local   # 改 DB_USER / DB_PASSWORD / ADMIN_PASSWORD

# 3. 启动后端（自动建表 + seed 默认 owner）
go run ./cmd/gokych

# 4. 启动前端（另一终端）
cd web
npm install
npm run dev
# 打开 http://localhost:3000
# 后台 http://localhost:3000/admin  →  admin / admin123
```

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
  -v $(pwd)/data:/app/data \
  gokych
```

---

## 配置

### 环境变量

| 变量                | 默认值                     | 说明                                    |
|---------------------|---------------------------|-----------------------------------------|
| `DB_HOST`           | `localhost`               | MySQL host                              |
| `DB_PORT`           | `3306`                    | MySQL port                              |
| `DB_USER`           | `gokych`                  | MySQL user                              |
| `DB_PASSWORD`       | `gokych`                  | MySQL password                          |
| `DB_NAME`           | `gokych`                  | MySQL database                          |
| `DB_CHARSET`        | `utf8mb4`                 |                                         |
| `DB_POOL_MIN`       | `2`                       | 连接池最小                              |
| `DB_POOL_MAX`       | `10`                      | 连接池最大                              |
| `DB_POOL_RECYCLE`   | `3600`                    | 连接回收秒数                            |
| `APP_PORT`          | `8000`                    | HTTP 监听端口                           |
| `GIN_MODE`          | `debug`                   | `debug` / `release` / `test`            |
| `SESSION_SECRET`    | `change-me-...`           | ⚠️  release 模式下必须改                |
| `ADMIN_USERNAME`    | `admin`                   | 首次启动 seed 的 owner 用户名           |
| `ADMIN_PASSWORD`    | `admin123`                | ⚠️  首次启动后立刻改                    |
| `DATA_DIR`          | `data`                    | 数据/上传/主题/插件目录                 |
| `TRUSTED_PROXIES`   | （空）                    | 逗号分隔的代理 CIDR/IP，空=不信任任何代理 |

### YAML 配置

`$DATA_DIR/settings/db.yaml` 可覆盖 MySQL 设置，YAML 字段缺失时回退到环境变量/默认值。
`$DATA_DIR/settings/settings.yml` 站点元数据（标题、ICP、主题）由后台 `/admin/settings` 维护。

---

## 目录结构

```
.
├── cmd/gokych/                # 入口
├── internal/
│   ├── api/                   # HTTP handler + 路由 + 中间件
│   │   ├── auth.go            #   登录/注册/登出
│   │   ├── admin.go           #   后台 CRUD
│   │   ├── articles.go        #   公共文章 API
│   │   ├── comments.go        #   评论/行评论
│   │   ├── ratings.go         #   评分
│   │   ├── files.go           #   文件管理
│   │   ├── site.go            #   站点公开配置
│   │   ├── middleware.go      #   auth/csrf/session
│   │   ├── requestid.go       #   X-Request-ID
│   │   ├── security.go        #   CSP/HSTS/X-Frame-Options
│   │   └── dberr.go           #   MySQL 1062 判重
│   ├── auth/                  # session / csrf / ratelimit / password
│   ├── content/               # 文章 + parsers
│   │   └── parsers/           #   markdown / wikidot / bbcode
│   ├── core/                  # db / schema / settings / metrics
│   └── typst/                 # typst CLI wrapper + cache
├── data/                      # 上传/头像/插件/主题/typst cache
├── web/                       # Next.js 15 前端
└── docs/                      # 阶段文档 / TODO
```

---

## API 一览

公开读：

| 方法 | 路径                                            | 说明                       |
|------|------------------------------------------------|----------------------------|
| GET  | `/api/site`                                    | 站点元数据 + 子站点链接    |
| GET  | `/api/home`                                    | 首页聚合（推荐+最近+通知） |
| GET  | `/api/articles?type=md&page=1`                 | 列表                       |
| GET  | `/api/articles/{type}/{slug}`                  | 详情                       |
| GET  | `/api/search?q=foo`                            | 全文搜索                   |
| GET  | `/api/labels` / `/api/labels/{tag}`            | 标签                       |
| GET  | `/api/articles/{type}/{slug}/comments`         | 评论                       |
| GET  | `/api/articles/{type}/{slug}/line-comments`    | 行评论                     |
| GET  | `/api/articles/{type}/{slug}/rating`           | 评分摘要                   |
| GET  | `/api/notifications`                           | 通知                       |

登录/CSRF：

| 方法 | 路径                       | 说明                          |
|------|---------------------------|-------------------------------|
| GET  | `/api/auth/me`            | 当前用户（或 null）           |
| GET  | `/api/auth/csrf`          | CSRF token + 数学验证码        |
| POST | `/api/auth/login`         | 用户名+密码+验证码             |
| POST | `/api/auth/logout`        | 退出                          |

写操作（CSRF + 登录）：

| 方法   | 路径                                                  | 说明            |
|--------|------------------------------------------------------|----------------|
| POST   | `/api/articles/{type}/{slug}/comments`               | 评论            |
| POST   | `/api/articles/{type}/{slug}/line-comments`          | 行评论          |
| POST   | `/api/articles/{type}/{slug}/rating`                 | 评分            |
| DELETE | `/api/articles/{type}/{slug}/rating`                 | 撤销评分        |
| POST   | `/api/articles?type=md`                              | 创建文章（admin）|
| PUT    | `/api/articles/{type}/{slug}`                        | 更新文章（admin）|
| DELETE | `/api/articles/{type}/{slug}`                        | 删除文章（admin）|
| /api/admin/*                                            | 后台 CRUD       |

所有响应都是 JSON，错误格式 `{"error": "..."}`。GET 成功直接返回数据对象/数组；写成功返回 `{"status":"ok", ...}` 或新建的对象。

---

## 安全

- 密码 bcrypt 12 轮，最多 72 字节
- 会话 cookie secure/httpOnly（release 模式）
- CSRF token 32 字节 crypto/rand
- 登录限流（每 (user,ip) 5/min 失败、20/min 失败/总 IP、10 次失败锁 15min）
- TrustedProxies 严格控制 X-Forwarded-For 解析
- Content-Security-Policy / X-Frame-Options / X-Content-Type-Options 统一中间件
- BBCode/Wikidot URL 与 CSS 注入过滤
- 文件上传 MIME 白名单 + 大小上限 10MB

---

## 开发

```bash
# 静态检查
go vet ./...
go test ./... -count=1
gofmt -l ./internal/ ./cmd/

# 前端
cd web
npm run lint
npm run build
```

---

## 扩展点

- **主题** — `data/themes/<name>/theme.yaml` + `static/theme.css`，后台选择。详细见 [`docs/主题开发指南.md`](docs/主题开发指南.md)
- **Typst** — `internal/typst/typst.go` 包装 `typst` CLI；写自己的 `.typ` 模块丢到文章目录里
- **API Key** — `internal/api/apikey.go`（待实现：CRUD + 中间件鉴权）
- **Passkey** — schema 已有 `webauthn_credentials` 表，待补注册/认证 handler

---

## License

MIT
