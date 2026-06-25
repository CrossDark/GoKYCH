# GoKYCH 部署方案 — Ubuntu 24 (后端) + Cloudflare Pages (前端)

> 范围：把 `main` 分支当前状态（≥ commit `90498db`，即 "feat(deploy):
> CORS middleware + absolute PUBLIC_URL for cross-origin frontends"
> 之后）以生产形态部署。后端单实例跑在 Ubuntu 24.04 LTS VM 上
> （systemd + nginx + MySQL 8），前端静态化部署到 Cloudflare Pages。

> 之前的版本里把"补 CORS + PUBLIC_URL"列为 §0 的阻塞项 —
> 那个 PR（`90498db`）已经合到 main 了，本方案按"已就位"来写。
> 如果你的部署起点早于那个 commit，参考那个 commit 的 message
> 里的前置条件清单。

---

## 0. 一句话总结

后端 systemd 起一个 Go binary，nginx 在前面收 TLS，MySQL 在同机；
前端 CF Pages 静态托管 Next.js；跨域用 CORS 中间件 + 绝对 PUBLIC_URL
解决。开始动手前先确认环境域名（`gokych.example.com` /
`api.example.com`）的 DNS 都能指到位。

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
# RPID 是 eTLD+1（无 scheme / port），浏览器按它来 scope passkey。
# 用前端实际访问的 host 即可（不是 API 域名），例如
# https://gokych.example.com。空则禁用 Passkey，登录页 503。
APP_DOMAIN=https://gokych.example.com

# ── 跨域 ──
# PUBLIC_URL：后端对外可访问的绝对基础 URL，拼接在 /uploads/* 和
# /avatars/* 路径前面，让 CF Pages 那边能直接 fetch 图片（不依赖
# Next.js 的 rewrite — CF Pages 没有那个代理）。
# CORS_ALLOWED_ORIGINS：允许跨域 fetch 的 origin 列表，逗号分隔。
# 带 cookie 的请求不能用通配符 origin（CORS 规范），所以必须显式列。
# 多个 origin 写一起即可。dev 阶段经常要同时跑前/后端两个端口，
# 把 http://localhost:3000 加上；纯 prod 环境就只留前端域名。
PUBLIC_URL=https://api.example.com
CORS_ALLOWED_ORIGINS=https://gokych.example.com

# ── 反向代理信任 ──
# nginx 在 127.0.0.1，所以信它（c.ClientIP() 才不会被打到 client 的
# 真实 IP 上，从而绕开 rate-limit / IP 检查）
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
  // 必要：让 next-on-pages 拿到 server.js。standalone 输出会把
  // node_modules 里实际用到的依赖打包成 .next/standalone/node_modules，
  // 比默认的 server build 小 ~95%。
  output: "standalone",

  // Dev rewrites 保留 — 它们在本地开发时把 /api/*、/uploads/*、
  // /avatars/* 代理到后端 8000 端口。生产模式下 frontend 不跑
  // Next.js server，所以 rewrites 不会执行；CORS + PUBLIC_URL
  // 已经把跨域问题解决了，不需要 rewrite。
  // （保留原 dev rewrites 即可，下面只是注释提示；不需要改代码。）

  // 允许 next-on-pages 的 edge runtime
  experimental: { runtime: "edge" },
};

export default nextConfig;
```

> Dev 的 rewrites 实际是「`web/next.config.ts:9-22`」里那条
> `source: "/api/:path*"` 等条目。**不要删** — dev 模式（`next dev`）
> 还在用它们，删了 dev 起来反而会 404。生产模式只是不读它们而已。

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

> `API_BASE_URL` 只影响服务端渲染时的 fetch（Next.js 15 在 server
> 组件里直接调 `getArticle()` 等），客户端 fetch 走 `relative`
> URL — 实际访问的是 CF Pages 的 origin，由浏览器自己 resolve 成
> `https://gokych.example.com/api/...`，再经 CORS 跨域到
> `https://api.example.com`。
>
> 上传/头像的 URL **不**走 `API_BASE_URL` — 后端 `PUBLIC_URL` 直接
> 在响应里返回绝对地址（commit `90498db`），浏览器拿到就是
> `https://api.example.com/uploads/xxx.jpg`，直接 fetch 即可。

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
| 上传 URL 相对路径 | 跨域下浏览器 404 | 已修：后端用 `PUBLIC_URL` 拼绝对路径（commit `90498db`），前端 `f.url` 直接用 |
| Passkey 突然 503 | `APP_DOMAIN` 改错 / RPID 跟前端 host 不匹配 | `journalctl -u gokych | grep passkey` 看 startup log；RPID 必须是 eTLD+1 |
| `next-on-pages` 兼容性 | Next.js 15 + 适配器 1.13+ 验证过；用前先在 PR preview URL 跑一遍 | Pages PR preview 是免费的，先开一个 dry-run |
| CORS preflight 走错中间件 | OPTIONS 请求如果被 CSRF 中间件拦了，浏览器永远收不到 204 → 实际 mutation 也发不出去 | CORS 已在 `csrfMiddleware` 之前安装，preflight 直接 204 短路（commit `90498db`） |

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
> 解决。`.env` 里 CORS_ALLOWED_ORIGINS / PUBLIC_URL / APP_DOMAIN
> 这三个是生产正确性的关键 — 改完 .env 必须 `systemctl restart gokych`。**

### 一键部署（macOS / Linux 都能跑）

不想手抄 §2 的话，仓库里有 `scripts/deploy-backend.sh`，从开发机一行命令搞定：

```bash
# 首次部署：自动建用户、装包、初始化 MySQL、写 systemd、签 TLS
./scripts/deploy-backend.sh

# 后续更新：只重传二进制 + 重启（.env 里的 DB_PASSWORD / SESSION_SECRET 自动保留）
./scripts/deploy-backend.sh --update

# ARM64 Ubuntu VM（AWS Graviton / 树莓派）
GOARCH=arm64 ./scripts/deploy-backend.sh
```

脚本是幂等的 — 大部分步骤检查"已就位就跳过"。跨平台编译用
`CGO_ENABLED=0 GOOS=linux`，产物是静态二进制，目标机器不需要
装 Go runtime。

---

## 附录 A：相关 commit 一览（按时间倒序）

| commit    | 主题                                                         |
|-----------|--------------------------------------------------------------|
| (pending) | chore: add scripts/deploy-backend.sh (一键部署)              |
| `90498db` | feat(deploy): CORS middleware + absolute PUBLIC_URL          |
| `192cbb9` | docs: deployment plan for Ubuntu 24 + Cloudflare Pages       |
| `1db5623` | docs(env): document APP_DOMAIN in .env.example              |
| `cef28c9` | docs: add TODO + integration/profile/unicode test scripts    |
| `aed9200` | feat(article): regular users can CRUD their own articles    |
| `7154585` | fix(auth): persist BackupEligible + require discoverable     |

`90498db` 是把"跨域可工作"这件事从方案层面落到代码的 PR — 部署前
请确认仓库 ≥ 这个 commit，否则需要先把缺的代码补上。`scripts/deploy-backend.sh`
是后续加的，把 §2 的手抄步骤封装成一条命令。

