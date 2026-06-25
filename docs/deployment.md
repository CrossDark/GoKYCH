# GoKYCH 部署方案 — Ubuntu 24 (后端) + Cloudflare Pages (前端)

> 范围：把 `main` 分支当前状态（commit `1db5623` 之后）以生产形态部署。
> 后端单实例跑在 Ubuntu 24.04 LTS VM 上（systemd + nginx + MySQL 8），
> 前端静态化部署到 Cloudflare Pages。整套方案在开始动手前需要先补两个
> 跨域改动（见 §0），否则生产环境 90% 的功能会坏在第一步。

---

## 0. 必须先做的代码改动（部署前）

跨域场景（CF Pages 和后端在不同域名）下，仓库当前状态有两个真坑，
不修直接部署就是各种 404 / CORS / 死链：

### 0.1 后端缺 CORS 中间件

`web/lib/api.ts` 的 `BASE` 在生产环境是 `https://api.example.com`，
浏览器从 `https://gokych.example.com` 调过去是**跨源**请求。`web/lib/api.ts`
走的是 `credentials: "include"`（带 cookie），按 CORS 规范这要求后端
显式 `Access-Control-Allow-Origin: <具体 origin>`，不能用 `*`。
而当前后端 `internal/api/router.go` 没装任何 CORS 中间件 → 浏览器
直接拦掉所有 `fetch`。

**修法**：在 `internal/api/router.go` 的 `r.Use(...)` 链路里加一个
`cors(allowedOrigins []string)` 中间件（建议实现而不是用第三方库 —
只有 ~30 行）。配置走环境变量 `CORS_ALLOWED_ORIGINS`，逗号分隔多个
origin（dev 是 `http://localhost:3000`，prod 是
`https://gokych.example.com`）。预检 (OPTIONS) 必须直接 204 返回，
不能走到业务 handler。

### 0.2 上传/头像 URL 是相对路径，跨域场景会 404

`internal/api/files.go` 的 upload 响应里 `url` 是 `"/uploads/xxx"`，
`internal/api/server.go` 静态服务也是 `/uploads`、`/avatars` 路径。
当前 dev 模式靠 `web/next.config.ts` 的 `rewrites` 把 `/uploads/*`
代理到后端，但生产在 CF Pages 上没有这个代理 — 浏览器会向
`gokych.example.com/uploads/xxx.jpg` 发请求，CF Pages 当然 404。

**修法（推荐）**：后端读新环境变量 `PUBLIC_URL`（如
`https://api.example.com`），在 upload/avatar 响应里把相对路径
改成 `PUBLIC_URL + "/uploads/xxx"`。前端无需改 — 浏览器拿到
绝对 URL 直接到后端域名拉。

如果暂时不想动后端，临时替代：把 CF Pages 的 `_redirects` /
`next.config.ts` 的 `rewrites` 配置成把 `/uploads/*` 和 `/avatars/*`
反向打到后端域名（CF Pages 支持跨域 rewrite，Workers Free 配额下
单文件 100MB 限制够用）。但这条路比改后端脆（如果换 CDN 就全坏），
且损失 Cloudflare 缓存/优化，建议长期走 §0.2 推荐方案。

> 建议把 §0.1 + §0.2 作为一个 PR 一起合，再开始 §1 部署。

---

## 1. 整体架构

```
                      ┌──────────────────────────────────────┐
                      │           Cloudflare                 │
   用户 ────────────▶ │  gokych.example.com  (Pages)         │
                      │      ↘ 跨域 fetch 带 cookie          │
                      │       api.example.com  ──────────────┼──┐
                      └──────────────────────────────────────┘  │
                                                                 │
                          ┌──────────────────────────────────────┴────────┐
                          │  Ubuntu 24.04 LTS VM                          │
                          │                                                │
                          │  :443 nginx (TLS, ACME via certbot)           │
                          │    ├─ /api/*       ─▶ :8000  gokych (systemd)│
                          │    ├─ /uploads/*   ─▶ :8000  gokych          │
                          │    └─ /avatars/*   ─▶ :8000  gokych          │
                          │                                                │
                          │  :3306  mysqld (systemd)                      │
                          │                                                │
                          │  /opt/gokych/         (binary + data)         │
                          │  /var/backups/gokych/ (daily mysqldump+rsync) │
                          └────────────────────────────────────────────────┘
```

域名分配（推荐）：
- `gokych.example.com` → CF Pages（前端）
- `api.example.com` → 你的 VM，nginx 收 443 转发 8000

API 都挂在 `api.example.com` 下，前端通过 `API_BASE_URL` 知道。

---

## 2. 后端（Ubuntu 24.04 LTS）

### 2.1 初始化

```bash
# 系统包
sudo apt update && sudo apt -y upgrade
sudo apt -y install mysql-server nginx certbot python3-certbot-nginx rsync

# 防火墙：只开 22 + 80 + 443
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable

# Go（编译用；运行时只用二进制，不需要常驻）
# 用 1.23+（仓库 go.mod 要求）
wget https://go.dev/dl/go1.23.4.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.4.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
```

### 2.2 MySQL

```bash
sudo mysql_secure_installation   # 跟着向导，root 密码选强
sudo mysql <<'SQL'
CREATE DATABASE gokych CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'gokych'@'127.0.0.1' IDENTIFIED BY 'STRONG-PASSWORD-HERE';
GRANT ALL PRIVILEGES ON gokych.* TO 'gokych'@'127.0.0.1';
FLUSH PRIVILEGES;
SQL
```

> MySQL 8.0 默认 `caching_sha2_password`，Go driver `github.com/go-sql-driver/mysql` 默认支持，但**不要**用 root 让 gokych 直连 — 上面这个 `gokych`@`127.0.0.1` 用户只对 gokych 库有权限就够了。

### 2.3 部署目录 + 二进制

```bash
sudo mkdir -p /opt/gokych/{bin,data}
sudo chown -R deploy:deploy /opt/gokych   # 假设有 deploy 用户，没有就建

# 本机编译后推上去（最干净：交叉编译出 linux/amd64 静态二进制）
# 在开发机：
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags='-s -w' -o gokych.linux.amd64 ./cmd/gokych
scp gokych.linux.amd64 deploy@api.example.com:/opt/gokych/bin/gokych
ssh deploy@api.example.com 'chmod +x /opt/gokych/bin/gokych'
```

> CGO_ENABLED=0 + alpine runtime 兼容（Dockerfile 用的也是这套），目标
> 机器不需要装 Go runtime。也可以在 VM 上 git clone 后 build，省去
> scp — 但前者留 build 痕迹更轻。

### 2.4 环境文件 `/opt/gokych/.env`

```ini
# ── 数据库 ──
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=gokych
DB_PASSWORD=STRONG-PASSWORD-HERE
DB_NAME=gokych
DB_CHARSET=utf8mb4
DB_POOL_MIN=2
DB_POOL_MAX=10

# ── 应用 ──
APP_PORT=8000
GIN_MODE=release

# ── 安全 ──
# 32+ 字节随机；不要 commit。`openssl rand -hex 32`
SESSION_SECRET=<paste-64-hex-chars-here>

# ── 默认管理员 ──
# 首次启动会创建；改密码后这两行可以无视
ADMIN_USERNAME=admin
ADMIN_PASSWORD=<一次性密码，首次登录后立刻改>

# ── 数据目录 ──
DATA_DIR=/opt/gokych/data

# ── WebAuthn / Passkey ──
# 必须等于前端实际访问的 origin（浏览器会字节级对比），否则登录页 503
APP_DOMAIN=https://gokych.example.com
# 上传/头像响应里用的绝对 URL 前缀（§0.2）
PUBLIC_URL=https://api.example.com

# ── CORS ──
# 逗号分隔；dev 加 http://localhost:3000，prod 只有 gokych.example.com
CORS_ALLOWED_ORIGINS=https://gokych.example.com

# ── 反向代理信任 ──
# nginx 在 127.0.0.1，所以信它
TRUSTED_PROXIES=127.0.0.1
```

`chmod 600 /opt/gokych/.env && chown deploy:deploy /opt/gokych/.env`。
`SESSION_SECRET` 必须随机（`openssl rand -hex 32`）；Gin 在 release
模式下用默认值会**直接 log.Fatal 拒绝启动**（见
`cmd/gokych/main.go:49`），这是有意为之的安全护栏。

### 2.5 systemd unit `/etc/systemd/system/gokych.service`

```ini
[Unit]
Description=GoKYCH backend
After=network.target mysql.service
Wants=mysql.service

[Service]
Type=simple
User=deploy
Group=deploy
WorkingDirectory=/opt/gokych
EnvironmentFile=/opt/gokych/.env
ExecStart=/opt/gokych/bin/gokych
Restart=on-failure
RestartSec=5s
# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/gokych/data
PrivateTmp=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gokych
sudo systemctl status gokych        # 看到 "server starting" + "passkey configured" 就行
sudo journalctl -u gokych -f        # 排错
```

### 2.6 nginx `/etc/nginx/sites-available/gokych`

```nginx
server {
    listen 80;
    server_name api.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name api.example.com;

    ssl_certificate     /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_stapling on;

    # 上传/下载：给大点 body 限制
    client_max_body_size 50m;

    # HSTS（一年 + subdomains，进了 HSTS preload list 后改不回来，谨慎）
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    # ── API ──
    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }

    # ── 上传 + 头像（gin.Static 已经在后端 8000 暴露了） ──
    location /uploads/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host $host;
        # 上传内容基本不会变 → 30 天缓存
        expires 30d;
        add_header Cache-Control "public, immutable";
    }
    location /avatars/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host $host;
        expires 7d;
        add_header Cache-Control "public";
    }

    # ── 健康检查（不进 access log） ──
    location = /healthz { proxy_pass http://127.0.0.1:8000/api/health; access_log off; }
}
```

```bash
sudo ln -s /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
sudo nginx -t
sudo certbot --nginx -d api.example.com --agree-tos -m you@example.com
sudo systemctl reload nginx
```

### 2.7 备份

```bash
# /etc/cron.d/gokych-backup
30 3 * * * deploy /opt/gokych/bin/backup.sh
```

`/opt/gokych/bin/backup.sh`：

```bash
#!/usr/bin/env bash
set -euo pipefail
TS=$(date -u +%Y%m%dT%H%M%SZ)
DEST=/var/backups/gokych
mkdir -p "$DEST"

# 1) MySQL 逻辑备份（mysqldump，gzip；一周一全量 + 每天增量，二进制日志启用再说）
mysqldump --single-transaction --quick --routines --triggers \
  -h 127.0.0.1 -u gokych -p"$DB_PASSWORD" gokych \
  | gzip > "$DEST/db-$TS.sql.gz"

# 2) 上传 + 配置 + 主题（增量同步到 S3 / 另一台机器）
rsync -a --delete /opt/gokych/data/ "$DEST/data-$TS/"

# 3) 保留 14 天
find "$DEST" -mtime +14 -name '*.gz' -delete
find "$DEST" -mtime +14 -name 'data-*' -type d -exec rm -rf {} +
```

把 `DB_PASSWORD` 从 `/opt/gokych/.env` 里读出来 export 一下，
`chmod 700 /opt/gokych/bin/backup.sh`。
重要：**`data/` 整个目录要备份** — 不光 uploads，还包括
`data/settings/`、`data/themes/`、`data/avatars/`（用户改的设置、
上传的主题、自定义头像都在里面）。

### 2.8 更新流程

```bash
# 开发机
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o gokych.linux.amd64 ./cmd/gokych
scp gokych.linux.amd64 deploy@api.example.com:/tmp/gokych.new

# VM
sudo systemctl stop gokych
sudo cp /opt/gokych/bin/gokych /opt/gokych/bin/gokych.prev   # 回滚保险
sudo cp /tmp/gokych.new /opt/gokych/bin/gokych
sudo systemctl start gokych
sudo journalctl -u gokych -n 50 --no-pager
# 冒烟：curl -fsS https://api.example.com/healthz
```

> 任何 schema 改动（commit 信息里有 `feat:`/`refactor:` 但涉及表结构
> 的）都需要先备份再升级；`internal/core/schema/schema.go` 里的
> `runMigrations` 会自动加列，但**不删列**、不收缩数据。

---

## 3. 前端（Cloudflare Pages）

### 3.1 项目结构 + 构建

仓库里 `web/` 已经是标准的 Next.js 15 app router 项目。CF Pages
对 Next.js 的支持走 `@cloudflare/next-on-pages` 适配器，把
`output: "standalone"` 的 Next.js 编译产物转成 Workers 兼容的格式。

**`web/next.config.ts` 改动**（部署前）：

```ts
import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",       // 必要：让 next-on-pages 拿到 server.js
  // 不再需要 dev 的 rewrites — 生产模式下前端直接 fetch API_BASE_URL
  async rewrites() { return []; },

  // 允许 next-on-pages 的 edge runtime
  experimental: { runtime: "edge" },
};

export default nextConfig;
```

**`web/package.json` 加脚本**：

```json
{
  "scripts": {
    "build:cf": "next build && next-on-pages"
  },
  "devDependencies": {
    "@cloudflare/next-on-pages": "^1.13.0"
  }
}
```

### 3.2 Cloudflare Pages 配置

在 Cloudflare 控制台 **Workers & Pages → Create → Pages → Connect to Git**：

| 字段              | 值                                                                 |
|-------------------|--------------------------------------------------------------------|
| Build command     | `cd web && npm ci && npm run build:cf`                             |
| Build output      | `web/.vercel/output/static`                                         |
| Root directory    | `web`                                                              |
| Node version      | `20`                                                               |

**环境变量**（在 Pages 项目 → Settings → Environment variables）：

| 变量             | Production                         | Preview (可选)              |
|------------------|------------------------------------|-----------------------------|
| `API_BASE_URL`   | `https://api.example.com`          | `https://api-stg.example.com` |
| `NODE_VERSION`   | `20`                               | `20`                        |

> `API_BASE_URL` 走 §0.2 推荐的「后端返回绝对 URL」方案时只影响
> 服务端渲染时的 fetch；浏览器里 fetch 也直接拼这个值，跨源请求
> 带 cookie 走 CORS 中间件放行。

### 3.3 自定义域名

1. Pages 项目 → **Custom domains** → `gokych.example.com`
2. CF 自动加 CNAME，等生效（~5 分钟）
3. SSL 自动签发（CF 一等公民，零配置）

### 3.4 预览环境（推荐）

CF Pages 每次 PR 自动开 preview URL，绑一个 staging 后端就够
了 — 比如 `api-stg.example.com`，配 `Preview` 环境变量。

---

## 4. DNS

| 记录 | 名称                       | 类型  | 值                                  |
|------|----------------------------|-------|-------------------------------------|
| A    | `api.example.com`          | A     | `<VM 公网 IP>`                      |
| AAAA | `api.example.com`          | AAAA  | `<VM IPv6>`（如有）                 |
| CNAME| `gokych.example.com`       | CNAME | `<your-project>.pages.dev`          |

> 如果 `example.com` 托管在 Cloudflare，CNAME 直接生效；否则
> 走传统 DNS。

---

## 5. 端到端验证清单

部署完后按这个顺序过一遍：

1. `curl -fsS https://api.example.com/healthz` → `{"db":"ok","status":"ok"}`
2. `curl -fsS https://gokych.example.com/` → 200 渲染首页（不要 curl /api/*，那是后端）
3. 浏览器开 `https://gokych.example.com/admin`，登录 admin
4. 创建一个 md 文章，看列表里出现，前台 `/md/<slug>` 能访问
5. **Passkey 登录**（最严苛）：开无痕窗口 → `/auth/login` →
   「使用 Passkey 登录」→ 浏览器弹出 authenticator → 登录成功 →
   URL 跳到 `/admin`
6. 评论、行评论、评分 各发一条；admin 后台能看到通知
7. 文件管理：上传一张图 → 复制「URL」列 → 新窗口打开能直接看到
8. `journalctl -u gokych` 没有 ERROR / WARN（除了首次启动的
   "session secret" 等已知一次性日志）

Passkey 特别说明：浏览器弹的认证框子里写的是 `localhost` 是因为
RPID 用了 `localhost` — 这只在 `APP_DOMAIN=localhost:3000` 时发生。
生产 `APP_DOMAIN=https://gokych.example.com` 会弹出来是
`gokych.example.com`，完全正确。**RPID 必须是 eTLD+1**（不能跨
主域），所以前端域名和后端域名必须是同一棵域树的子域（都挂在
`example.com` 下）。

---

## 6. 监控（够用就行）

最小集：

- **uptime**：Cloudflare 自己的 [Uptime Monitoring](https://developers.cloudflare.com/fundamentals/turnstile/)（免费，配置 5 分钟）
  监控 `https://api.example.com/healthz` 和 `https://gokych.example.com/`
- **日志**：后端写 stdout → journald，配一个 rsyslog → 远程 syslog
  （papertrail/betterstack 都行）或者直接 `journalctl --since today -u gokych`
  每周翻一次
- **磁盘**：`/opt/gokych/data` 涨得最厉害的就是 `uploads/`，加个
  简单的 `df` 检查到 cron，>80% 告警
- **MySQL**：mysqldump 失败 → 邮件告警（cron + mailx 拼一下）

---

## 7. 已知坑（deploy 之前先扫一眼）

| 坑 | 说明 | 规避 |
|----|------|------|
| Passkey 需要 HTTPS | WebAuthn 协议硬性要求 secure context | CF Pages 自动 HTTPS；VM 走 certbot |
| RPID 域要匹配 | RPID 必须是 eTLD+1，且必须等于 `APP_DOMAIN` 解析出的 host | 前/后端都用 `*.example.com` 子域 |
| Session cookie `Secure` | release 模式自动开 Secure，开发用 HTTP 测 cookie 不会带 | `.env` 一定设 `GIN_MODE=release` |
| MySQL `caching_sha2_password` | Go driver 默认支持，但需要 MySQL 8.0+ | 已在 docker-compose 锁定 8.0 |
| 时间戳时区 | `created_at` 走 MySQL server time；用 UTC | `data/` 里 `settings.yml` 改 timezone 或忽略（展示走浏览器 locale） |
| Uploads 体积 | nginx 默认 `client_max_body_size 1m` | 上面 nginx 配置改成 `50m` |
| 数据目录权限 | systemd `ProtectSystem=strict` 不给 /opt/gokych/data 写权限就起不来 | 配 `ReadWritePaths=/opt/gokych/data`（已加） |
| 跨域 cookie | CORS + `credentials: "include"` 不允许 `*` 源 | 后端用 `CORS_ALLOWED_ORIGINS` 显式列 |
| 上传 URL 相对路径 | 跨域下浏览器 404 | 改后端用 `PUBLIC_URL` 拼绝对路径（§0.2） |
| `next-on-pages` 兼容性 | Next.js 15 + 适配器 1.13+ 验证过；用前先在 PR preview URL 跑一遍 | Pages PR preview 是免费的，先开一个 dry-run |

---

## 8. 回滚预案

| 场景                    | 怎么办                                              |
|-------------------------|------------------------------------------------------|
| 后端起不来              | `sudo systemctl restart gokych`；不行就 `cp gokych.prev gokych` 然后 restart |
| Passkey 突然挂          | 看 `journalctl -u gokych | grep passkey`；`APP_DOMAIN` 改错就 503 |
| 前端构建失败            | CF Pages 会保留上一次成功部署，UI 上点 "Rollback to this deployment" |
| 数据库升级挂了          | `mv /var/backups/gokych/db-latest.sql.gz /tmp/`，从备份恢复（drop + create + 灌入） |
| 整个翻车                | 重新跑 §2 整套；10 分钟内能恢复（VM 不挂 + DNS 不挂就 OK） |

---

## 9. 一行话总结

> **Ubuntu 24 VM 上跑 gokych 单实例（systemd + nginx + MySQL 8），
> CF Pages 静态托管 Next.js，跨域用 CORS + `PUBLIC_URL` 绝对 URL
> 解决。先合 §0 的 PR，再按 §2→§3 顺序部署。**
