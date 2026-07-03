# GoKYCH 部署方案 — Ubuntu 24 (后端) + 腾讯云 EdgeOne Makers (前端)

> 范围：把 `main` 分支当前状态以生产形态部署。后端单实例跑在
> Ubuntu 24.04 LTS VM 上（systemd + nginx + MySQL 8），前端 Next.js
> 通过腾讯云 **EdgeOne Makers** 连 GitHub 仓库自动构建部署到边缘
> 节点（零配置识别 Next.js 全栈能力，保留 SSR）。前端 SSR 与浏览器
> 均跨域调 `api.kych.net`，由后端 CORS 中间件兜底。

> 之前的版本里把"补 CORS + PUBLIC_URL"列为 §0 的阻塞项 —
> 那个 PR（`90498db`）已经合到 main 了，本方案按"已就位"来写。
> 如果你的部署起点早于那个 commit，参考那个 commit 的 message
> 里的前置条件清单。

---

## 0. 一句话总结

后端 systemd 起一个 Go binary，nginx 在前面收 TLS；MySQL 在同机；
前端由 EdgeOne Makers 连 GitHub 仓库自动构建部署到边缘节点（Next.js
SSR 跑在边缘）。`kych.net` 和 `api.kych.net` 直连 VM（A 记录 + certbot），
`eo.kych.net` 走 EdgeOne（CNAME + 平台自动证书）。浏览器跨域 fetch 到
`api.kych.net`，CORS 由后端 `CORS_ALLOWED_ORIGINS` 兜底。

**VM 上不跑 Node。** 所有前端构建 / SSR 容器由 EdgeOne Makers 平台管理。

---

## 1. 整体架构

```
                       ┌─────────────────────────────────────────┐
                       │          腾讯云 EdgeOne Makers           │
   用户 ────────────▶  │  eo.kych.net  (Next.js SSR 边缘运行)     │
                       │      ↘ SSR fetch 到 api.kych.net        │
                       └─────────────────┬────────────────────────┘
                                         │ 跨域 fetch (CORS)
                           ┌─────────────▼────────────────────────┐
                           │  Ubuntu 24.04 LTS VM                 │
                           │                                        │
                           │  :443 nginx (TLS via certbot)         │
                           │    ├─ api.kych.net                     │
                           │    │   ├─ /api/*     ─▶ :8000 gokych   │
                           │    │   ├─ /uploads/* ─▶ :8000 gokych   │
                           │    │   └─ /avatars/* ─▶ :8000 gokych   │
                           │    └─ kych.net  → 301 eo.kych.net      │
                           │                                        │
                           │  :3306 mysqld (systemd)               │
                           │  /opt/gokych/  (binary + data)        │
                           └────────────────────────────────────────┘
```

DNS 分配：
- `kych.net` → A 记录直连 VM；nginx 301 跳转到 `https://eo.kych.net`
- `api.kych.net` → A 记录直连 VM；接收所有后端请求
- `eo.kych.net` → CNAME 到 EdgeOne Makers 自动签发的加速域名

浏览器 + SSR 的 API fetch 都打 `https://api.kych.net/api/...`，由
后端 `CORS_ALLOWED_ORIGINS=https://eo.kych.net` 兜底（commit `90498db`
的 CORS 中间件已在 `csrfMiddleware` 之前安装，preflight 直接 204）。
SSR 跨域 + `credentials: "include"` 时 Session cookie 由 Next.js
通过 `next/headers` 读出后在每次 fetch 里手工 `Cookie:` 转发
（见 `web/lib/api.ts` 的 `getServerCookies()`），不需要 Next.js 反代。

---

## 2. 后端（Ubuntu 24.04 LTS）

### 2.1 初始化

```bash
# 系统包
sudo apt update && sudo apt -y upgrade
sudo apt -y install mysql-server nginx certbot python3-certbot-nginx rsync

# 中文字体 — typst 编译 PDF 时必须。如果不装,所有 typst 文章的
# PDF 下载会显示成方块(HTML 路径不受影响,因为前端会用浏览器系统
# 字体 fallback;PDF 路径 typst 必须用本地字体,本地没有就 missing glyph)。
# `fonts-noto-cjk` 提供 Noto Serif/Sans CJK SC,覆盖正文和 heading;
# `fonts-wqy-microhei` 是更轻量的兜底,文章注释里出现生僻字时备选。
sudo apt -y install fonts-noto-cjk fonts-wqy-microhei

# 防火墙：只开 22 + 80 + 443
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable

# Go（编译用；运行时只用二进制，不需要常驻）
# 用 1.26+（仓库 go.mod 要求 go 1.26.4）
wget https://go.dev/dl/go1.26.4.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.26.4.linux-amd64.tar.gz
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
scp gokych.linux.amd64 deploy@api.kych.net:/opt/gokych/bin/gokych
ssh deploy@api.kych.net 'chmod +x /opt/gokych/bin/gokych'
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
# https://eo.kych.net。空则禁用 Passkey，登录页 503。
APP_DOMAIN=https://eo.kych.net

# ── 跨域 ──
# PUBLIC_URL：后端对外可访问的绝对基础 URL，拼接在 /uploads/* 和
# /avatars/* 路径前面，让 EdgeOne 边缘 SSR / 浏览器直接拿到图片
# （不走 Next.js rewrite — 边缘 SSR 没有反代）。
# CORS_ALLOWED_ORIGINS：允许跨域 fetch 的 origin 列表，逗号分隔。
# 带 cookie 的请求不能用通配符 origin（CORS 规范），所以必须显式列。
PUBLIC_URL=https://api.kych.net
CORS_ALLOWED_ORIGINS=https://eo.kych.net

# SESSION_COOKIE_DOMAIN：把 session cookie 绑到父域（必须带前导点 . ），
# 让前端 (eo.kych.net) 和后端 (api.kych.net) 都能看到。前端 SSR 的
# cookies() 才能拿到 session 转发给后端，后端 CurrentUser 才不会把
# 登录用户当成匿名 —— 否则已评分的文章滑块会停在 0.0 ("你的评分 --")，
# 平均分和详细评分正常。GIN_MODE=release 时 Secure 自动 = true，
# 配合 Domain=.kych.net + SameSite=None 跨域带 cookie 才合规。
SESSION_COOKIE_DOMAIN=.kych.net

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

### 2.6 nginx + SSL 证书（鸡生蛋问题）

#### 鸡生蛋问题

nginx 启动 `listen 443 ssl` 需要 SSL 证书文件存在；certbot 用 HTTP-01
challenge 签证书需要 nginx 的 `listen 80` 端口能响应验证请求。
如果先写包含 `ssl_certificate` 的配置，`nginx -t` 会因为证书文件不存在
**直接失败退出**，nginx 起不来，certbot 也无法验证域名，死锁。

#### 解法：HTTP-first

先只写 `listen 80` 的纯 HTTP 配置（**不引用任何证书文件**），nginx 能
正常启动；然后 `certbot --nginx` 自动用 80 端口跑 HTTP-01 验证、签
证书、往配置里自动插入 `listen 443 ssl` 块 + 证书路径 + 80→443 跳转，
最后 reload nginx。全程零手动编辑证书路径。

#### 配置 `/etc/nginx/sites-available/gokych`

只配两个域名（**`eo.kych.net` 不在这里** — 它走 EdgeOne 边缘，nginx
上不接这个 server 块）：

```nginx
# ─ api.kych.net ─ 后端 API + 静态资源 ─
server {
    listen 80;
    server_name api.kych.net;

    client_max_body_size 50m;

    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
    location /uploads/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host $host;
        expires 30d;
        add_header Cache-Control "public, immutable";
    }
    location /avatars/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host $host;
        expires 7d;
        add_header Cache-Control "public";
    }
    location = /healthz { proxy_pass http://127.0.0.1:8000/api/health; access_log off; }
}

# ─ kych.net ─ 主域名 301 跳转到 EdgeOne 边缘的前端 ─
server {
    listen 80;
    server_name kych.net;
    return 301 https://eo.kych.net$request_uri;
}
```

#### 启动 + 签证书

```bash
sudo ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t          # ← 这步现在能过，因为配置里没有任何 ssl_certificate 引用
sudo systemctl restart nginx    # nginx 在 80 端口正常运行

# certbot --nginx 会:
#   1. 用 80 端口跑 HTTP-01 验证（nginx 已在响应）
#   2. 签发 2 个域名的证书（api + main；eo 不签，由 EdgeOne 平台托管）
#   3. 自动往 nginx 配置里插入 listen 443 ssl + 证径路径
#   4. 自动给每个 listen 80 块加 return 301 → https
#   5. reload nginx
sudo certbot --nginx \
  -d api.kych.net -d kych.net \
  --non-interactive --agree-tos -m you@kych.net --redirect
```

> **`--redirect`** 让 certbot 顺手给每个 80 块加 301 跳转到 443，
> 不用手动编辑。签好后 `nginx -t` 看一眼完整配置会有 4 个 server
> 块（2 个 80 跳转 + 2 个 443 ssl）。
>
> 续期：certbot 装好后自动加了一条 systemd timer
> (`certbot.timer`)，每天检查一次，到期前 30 天自动续。不用管。

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
scp gokych.linux.amd64 deploy@api.kych.net:/tmp/gokych.new

# VM
sudo systemctl stop gokych
sudo cp /opt/gokych/bin/gokych /opt/gokych/bin/gokych.prev   # 回滚保险
sudo cp /tmp/gokych.new /opt/gokych/bin/gokych
sudo systemctl start gokych
sudo journalctl -u gokych -n 50 --no-pager
# 冒烟：curl -fsS https://api.kych.net/healthz
```

> 任何 schema 改动（commit 信息里有 `feat:`/`refactor:` 但涉及表结构
> 的）都需要先备份再升级；`internal/core/schema/schema.go` 里的
> `runMigrations` 会自动加列，但**不删列**、不收缩数据。

---

## 3. 前端（EdgeOne Makers）

### 3.1 形态选择说明

EdgeOne Makers（腾讯云"全栈应用托管"产品，等价于 Vercel / Cloudflare
Pages + Functions）会**自动识别仓库根有 `package.json` + `next` 依赖**，
用平台内置的 Next.js 构建器编译并部署到边缘节点，SSR / API Route /
Middleware 全部保留。我们这边 zero config — 不用写 EdgeOne adapter，
不用换 `@cloudflare/next-on-pages`，代码里 `cookies()` / `headers()`
直接可用。

代价：EdgeOne 平台帮我们管了 Node runtime / 构建流水线 / 边缘网络，
**VM 上不再跑任何 Node**。前端发布 = `git push main` → 平台 webhook
自动构建 → 等 30-60 秒 → 全球边缘生效。

### 3.2 在 EdgeOne Makers 控制台接入仓库

控制台路径与字段名以腾讯云实际界面为准（平台会迭代），下面只列
客观不变的核心配置项：

1. **新建应用**：选择"从 Git 仓库导入"，授权 GitHub，定位到
   `CrossDark/GoKYCH` 仓库，根目录留空（默认就是 `web/`，如果
   EdgeOne 不自动识别就到"构建设置"里把 Root Directory 改成 `web/`）。
2. **环境变量**（生产）：
   - **`NEXT_PUBLIC_API_BASE_URL` = `https://api.kych.net`** ← **必填，缺失会让构建直接 fail（`web/next.config.ts` 起手就 throw）。** 没配它的话每个文章详情页都会静默 fallback 到 `http://localhost:8000` 然后 ECONNREFUSED,被 catch block 渲染成"文章不存在"——而且 SSR log 只有一个裸 `ECONNREFUSED` 没有提示。配置错了能在控制台"环境变量"里看；构建后才能改，需要重新触发部署。
   - 其他可选：`API_BASE_URL` = `https://api.kych.net`（SSR 路径
     备用，`apiUrl()` 已经优先读 `NEXT_PUBLIC_*`）
3. **构建命令 / 输出目录**：留平台默认（`next build`，输出到
   `.next/`）。**不要**手动指定 `output: "standalone"` — 我们仓库
   里 `web/next.config.ts` 已经把它改成条件 opt-in（仅当
   `STANDALONE=1` 才开），Makers 不会设这个环境变量，平台用自己
   默认的 SSR 模式构建。
4. **自定义域名**：添加 `eo.kych.net`。EdgeOne 自动签发 / 续期
   该域名的边缘 HTTPS 证书，并在边缘绑好 CNAME 目标值。把这个
   CNAME 加到 DNS（见 §5）。
5. **回源 / 函数配置**：留默认。Makers 自动处理 SSR 路由，
   不用配任何 rewrites。
6. **预览环境**（可选但推荐）：开启 "Preview Deployments"，
   每个 PR 自动得到一个 `pr-<num>.eo.kych.net` 预览地址。
   这条对带 `eo` 前缀的二级域很合适。

### 3.3 发布流程

代码改动后：

```bash
git push origin main   # EdgeOne webhook 触发自动构建
# 在 Makers 控制台"部署历史"看到进度，30-60 秒后状态变绿
# 验证：curl -fsS https://eo.kych.net/api/health   ← 这是 EdgeOne
#   边缘的 SSR Node 在同 origin 内部 ping 后端；不通就说明 NEXT_PUBLIC_API_BASE_URL 没配对
```

> **不要**在 VM 上跑 `next build` / 上传 standalone 产物 — EdgeOne
> Makers 自己构建。VM 上只放后端二进制和 nginx。

### 3.4 自定义错误的快速回滚

如果某次发布挂了：
- **EdgeOne Makers 控制台 → 部署历史 → 上一条 → 「回滚」**：30 秒内
  全球回到上一个 commit 的产物
- **代码层面**：回滚 commit + `git push`，让 Makers 重新构建并替换

都不需要碰 VM。

### 3.5 为什么浏览器 fetch 能跨域 + 带 cookie

`web/lib/api.ts` 里所有 fetch 都通过 `apiUrl(path)` helper 走：

```ts
const BASE =
  process.env.NEXT_PUBLIC_API_BASE_URL ||
  (typeof window === "undefined" ? process.env.API_BASE_URL || "http://localhost:8000" : "");

export function apiUrl(path: string): string {
  if (!path.startsWith("/")) path = "/" + path;
  return `${BASE}${path}`;
}
```

- **生产**：BASE = `https://api.kych.net`，所有 fetch 走绝对 URL
- **dev**：`next dev` 没设 `NEXT_PUBLIC_*`，BASE 在浏览器侧 = 空，
  fetch 走相对路径，被 `web/next.config.ts` 的 rewrites 转到
  `localhost:8000`

跨域带 cookie 的关键：
- 前端 fetch 加了 `credentials: "include"`
- 后端 CORS 中间件（`internal/api/cors.go`，commit `90498db`）的
  `Access-Control-Allow-Origin` 回写前端实际 Origin，且
  `Access-Control-Allow-Credentials: true`
- preflight OPTIONS 在 CSRF middleware 之前短路（commit `90498db`）
- Session cookie 域：`api.kych.net`（后端 host），浏览器对
  `api.kych.net` 的 fetch 自动带上

## 4. 前端备选：Cloudflare Workers（OpenNext）部署

如果希望前端部署在 Cloudflare 而非 EdgeOne，可以使用 `@opennextjs/cloudflare`
将 Next.js 构建为 **Cloudflare Workers**（Workers Paid 计划，免费计划 Worker 体积上限 3 MiB 不够）。

> **前置条件**：
> - Node.js 18+（推荐 20 LTS）
> - 一个 Cloudflare 账号（已注册 Workers Paid 计划，$5/月）
> - 后端 Go 服务已部署且有公网 HTTPS 地址（如 `https://api.kych.net`）
> - macOS / Linux 开发环境（Windows 建议用 WSL2）

---

### 4.1 仓库内置文件说明

仓库已为 Cloudflare 部署准备好以下文件（**无需手动创建**）：

| 文件 | 作用 |
|------|------|
| `web/wrangler.jsonc` | Wrangler 配置（Worker 名称为 `creater-rule-web`，`name` 和 `WORKER_SELF_REFERENCE.service` 已同步；Cloudflare CI 会自动覆盖 `name`，但**不会**覆盖 `services[].service`，因此两个字段必须显式与 Dashboard 中的 Worker 名称保持一致） |
| `web/open-next.config.ts` | OpenNext Cloudflare 适配器配置（必需，esbuild 构建时读取） |
| `web/.dev.vars` | 本地开发环境变量（`NEXTJS_ENV=development`） |
| `web/public/_headers` | Cloudflare Pages 静态资源缓存头 |
| `web/next.config.ts` | 末尾可选加载 `initOpenNextCloudflareForDev()`（不影响 EdgeOne/Docker） |
| `web/middleware.ts` | CSP nonce 生成（已使用 Web Crypto API，兼容 Edge Runtime） |
| `.gitignore` | 已包含 `.open-next/`、`.wrangler/`、`.dev.vars`、`.env.local` |

`@opennextjs/cloudflare` 和 `wrangler` 已添加到 `dependencies`（确保云端生产构建时也会安装），`npm install` 时会自动安装。
Worker 名称通过 `web/wrangler.jsonc` 静态配置（OpenNext 构建阶段不支持环境变量插值），
`name` 和 `WORKER_SELF_REFERENCE.service` 两个字段必须完全一致且与 Cloudflare Dashboard 中的 Worker 名称相同
（详见 §4.5）。

---

### 4.2 完整部署步骤

Cloudflare 支持两种部署方式，任选其一即可：

- **方式A：CLI 命令行部署**（§4.2.1）— 本地运行 wrangler 部署，适合开发者和CI/CD
- **方式B：Dashboard 网页端 Git 集成部署**（§4.2.2）— 在 Cloudflare 控制台连接 GitHub 仓库，每次 push 自动构建部署，无需本地安装 Node.js

---

#### 4.2.1 方式A：CLI 命令行部署

##### 步骤A1：准备后端

确保后端 Go 服务已部署并可从公网访问，且已配置：
- HTTPS 证书（Let's Encrypt 即可）
- CORS 允许你的 Cloudflare Workers 域名

后端 `.env` 中添加（**必须**，否则跨域请求被阻止）：

```bash
# 后端服务器 /opt/gokych/.env
CORS_ALLOWED_ORIGINS=https://你的-worker名.你的账号.workers.dev,https://你的自定义域名
```

改完后重启：`sudo systemctl restart gokych`

##### 步骤A2：克隆代码并安装依赖

```bash
git clone <你的仓库地址> GoKYCH
cd GoKYCH/web

# 安装前端依赖（必须）
npm install
```

> `@opennextjs/cloudflare` 和 `wrangler` 已在 `dependencies` 中，`npm install` 会自动安装，
> 无需额外操作。

##### 步骤A3：配置前端环境变量

在 `web/` 目录创建 `.env.local` 文件（**必须**，已在 .gitignore 中，不会被提交）：

```bash
# web/.env.local
NEXT_PUBLIC_API_BASE_URL=https://api.kych.net
```

> **关键提醒**：`NEXT_PUBLIC_API_BASE_URL` 是 **Next.js 构建时环境变量**，
> 会被 webpack 内联到客户端 JS bundle 中。必须在构建阶段设置
> （通过 `.env.local` 或 shell 环境变量）。**不能** 用 `wrangler secret put` 设置
> （那是运行时变量，构建阶段读不到，会导致前端所有 API 请求都指向 localhost）。

##### 步骤A4：登录 Cloudflare

```bash
# 在 web/ 目录下
npx wrangler login
```

浏览器会自动打开 Cloudflare 授权页面，点击「Allow」授权 Wrangler 管理你的 Workers。

验证登录状态：
```bash
npx wrangler whoami
```

##### 步骤A5（可选）：本地预览

在部署到 Cloudflare 之前，可以先在本地预览 Workers 运行时效果：

```bash
cd web/
npm run cf:preview
```

Wrangler 会启动一个本地 Worker 模拟器（通常在 `http://localhost:8788`），
你可以测试 SSR、API 请求、登录等功能。

> 注意：本地预览模式下 `NEXT_PUBLIC_API_BASE_URL` 仍从 `.env.local` 读取，
> 确保后端地址可访问（本地开发用 `http://localhost:8000` 或公网地址均可）。

##### 步骤A6：构建并部署

```bash
cd web/

# 构建并部署（使用 wrangler.jsonc 中配置的 Worker 名称）
npm run cf:deploy

# 如果需要自定义 Worker 名称，先修改 web/wrangler.jsonc 中的 name 和 services[0].service，
# 确保两者完全一致且与 Cloudflare Dashboard 中的 Worker 名称匹配，提交后再运行部署命令。
# 注意：Cloudflare Workers Builds（Git 连接部署）会自动用 Dashboard 中的 Worker 名称
# 覆盖 name 字段，但不会覆盖 services[].service，所以两个字段都必须显式设置正确。
```

部署成功后，Wrangler 会输出类似：

```
Total Upload: 2295.89 KiB / gzip: 612.34 KiB
Uploaded created-rule-front (3.42 sec)
Deployed created-rule-front triggers (0.78 sec)
  https://created-rule-front.你的账号.workers.dev
```

打开输出的 URL（如 `https://created-rule-front.你的账号.workers.dev`）验证网站。

---

#### 4.2.2 方式B：Cloudflare Dashboard 网页端 Git 集成部署

这种方式不需要在本地安装 Node.js/wrangler，完全在 Cloudflare 网页控制台操作。
Cloudflare 的 Workers Builds 会在服务器上自动拉取代码、安装依赖、构建并部署。
每次 push 到 GitHub 主分支会自动触发重新部署（类似 EdgeOne Makers）。

##### 步骤B1：准备后端

与方式A相同：确保后端已部署、HTTPS 可用、CORS 已配置 Workers 域名。

##### 步骤B2：推送代码到 GitHub

确保代码已推送到 GitHub 仓库（公开或私有均可，Cloudflare 支持私有仓库）。
确保 `web/wrangler.jsonc`、`web/open-next.config.ts` 等配置文件已提交到仓库。

##### 步骤B3：在 Cloudflare 控制台创建 Worker 并连接 Git

1. 登录 [Cloudflare Dashboard](https://dash.cloudflare.com/)
2. 左侧菜单点击 **Workers & Pages**
3. 点击 **Create application**（创建应用）
4. 选择 **Workers** 标签页（不是 Pages）
5. 点击 **Deploy using Cloudflare's build system**（或 "Connect to Git"）
   > 如果看不到此选项，先在 Workers & Pages 页面右上角确认账号已升级到
   > Workers Paid（$5/月计划，免费计划不支持 Git 构建）
6. 选择你的 Git 提供商（**GitHub** 或 GitLab），点击 **Connect**
7. 授权 Cloudflare 访问你的仓库（可以选择所有仓库或仅指定仓库）
8. 选择 GoKYCH 仓库，点击 **Begin setup**

##### 步骤B4：配置构建设置

在配置页面填写以下信息：

| 配置项 | 值 |
|--------|-----|
| **Production branch**（生产分支） | `main`（或你的主分支名） |
| **Root directory**（根目录） | `web`（**必须**，因为 wrangler.jsonc 在 web/ 子目录） |
| **Build command**（构建命令） | `npm run cf:build` |
| **Deploy command**（部署命令） | 留空（Cloudflare 自动检测 wrangler.jsonc 并执行 `wrangler deploy`） |

> **注意**：
> - Root directory 必须设为 `web`，因为 `wrangler.jsonc` 和 `package.json` 在 `web/` 目录下。
>   如果设为仓库根目录，Cloudflare 找不到 wrangler 配置文件会报错。
> - Build command 不能直接写 `npm run deploy`（那会在构建步骤中就尝试部署，导致权限错误）。
>   正确做法是 build command 只做构建（使用本地 npm scripts，避免 npx 网络问题），部署由 Cloudflare 自动通过 wrangler 完成。
> - 不要使用 `npx @opennextjs/cloudflare build`，因为 npx 会临时下载包，可能因网络问题或版本不一致导致构建失败。

##### 步骤B5：配置环境变量

在同一配置页面，找到 **Environment variables**（环境变量）部分，添加：

| 变量名 | 值 | 说明 |
|--------|-----|------|
| `NEXT_PUBLIC_API_BASE_URL` | `https://api.kych.net` | **构建时必需**，后端API地址 |

> - `NEXT_PUBLIC_API_BASE_URL` 必须在 **Builds** 的环境变量中设置（不仅仅是 Worker runtime 的环境变量），
>   因为它是构建时变量，需要在构建阶段被 webpack 读取。
> - 如果需要设置 Secrets（如其他密钥），在同一页面的 **Secrets** 部分添加（加密存储，不出现在日志中）。
> - 请勿使用 `wrangler secret put` 命令设置 `NEXT_PUBLIC_*` 变量（那是运行时变量，构建阶段读不到）。
> - Worker 名称如需自定义，需在部署前修改 `web/wrangler.jsonc` 中的 `name` 和 `services[0].service` 字段，
>   并将修改提交到 Git（不支持通过环境变量动态设置，因为 OpenNext 构建阶段不解析 wrangler.jsonc 中的环境变量插值）。

##### 步骤B6：部署

点击 **Save and Deploy**（保存并部署）。Cloudflare 将：
1. 拉取 GitHub 仓库代码
2. 进入 `web/` 目录
3. 执行 `npm install`（自动检测 package-lock.json，安装所有 dependencies）
4. 执行 `npm run cf:build`（构建，使用本地依赖，避免 npx 网络问题）
5. 执行 `wrangler deploy`（部署，读取 wrangler.jsonc）
6. 首次部署通常需要 2-5 分钟

构建日志可以在 Worker 详情页的 **Deployments**（部署）标签页查看。

部署成功后，Cloudflare 会分配一个 `https://<worker-name>.<你的账号>.workers.dev` 地址。

##### 步骤B7：后续自动部署

配置完成后，**每次 push 到 main 分支**都会自动触发构建和部署，无需任何手动操作。
如需查看构建日志或回滚：Workers & Pages → 你的 Worker → Deployments 标签页。

如果需要手动触发重新部署（不 push 代码）：Worker 详情页 → Deployments → 点击右上角 **Create deployment** → 选择最新 commit 部署。

---

### 4.3 自定义域名绑定（可选但推荐）

Workers 默认分配的 `xxx.workers.dev` 域名在国内访问可能不稳定，建议绑定自定义域名：

1. **DNS 托管到 Cloudflare**：你的域名（如 `kych.net`）必须使用 Cloudflare DNS
2. **Cloudflare Dashboard** → Workers & Pages → 你的 Worker（如 `created-rule-front`）
   → Settings → Triggers → Custom Domains → Add Custom Domain
3. 输入你想绑定的子域名，如 `cf.kych.net`，点击「Add Custom Domain」
4. Cloudflare 自动配置 DNS 和 SSL 证书，几分钟后即可通过 `https://cf.kych.net` 访问

绑定自定义域名后，**必须** 更新后端 CORS：

```bash
# 后端 /opt/gokych/.env
CORS_ALLOWED_ORIGINS=https://cf.kych.net,https://created-rule-front.你的账号.workers.dev
sudo systemctl restart gokych
```

重新构建部署前端（因为 `NEXT_PUBLIC_API_BASE_URL` 是构建时变量——不，等一下：
如果 API 地址没变，**不需要**重新构建，只需在 Cloudflare 控制台绑定域名即可）。

---

### 4.4 常用 npm scripts 说明

| 命令 | 说明 |
|------|------|
| `npm run dev` | Next.js 开发服务器（localhost:3000），连接本地后端 |
| `npm run build` | 纯 Next.js build（供 EdgeOne/Docker 使用，不做 OpenNext 转换） |
| `npm run cf:build` | OpenNext 构建（先生成 `.next/`，再转换为 `.open-next/` Worker 格式） |
| `npm run cf:deploy` | 构建并部署到 Cloudflare Workers（= cf:build + 部署） |
| `npm run cf:preview` | 本地预览 Workers 运行时（自动构建+启动本地模拟器） |
| `npm run typecheck` | TypeScript 类型检查 |
| `npm run lint` | ESLint 检查 |

---

### 4.5 自定义 Worker 名称

`web/wrangler.jsonc` 使用静态名称配置（OpenNext 构建阶段不支持环境变量插值）：

```jsonc
"name": "creater-rule-web",
"services": [
  {
    "binding": "WORKER_SELF_REFERENCE",
    "service": "creater-rule-web"
  }
]
```

如需自定义 Worker 名称（例如避免与已有 Worker 冲突，或 Dashboard 中已创建了不同名称的 Worker），**必须同时修改以下两个字段**，否则会出现 10143 错误：

1. `"name": "your-worker-name"`
2. `"services[0].service": "your-worker-name"`

> **重要**：
> - 修改后必须将 `web/wrangler.jsonc` 的变更提交到 Git，云端构建时才能生效。
> - `WORKER_SELF_REFERENCE` 是 OpenNext 用于 ISR（增量静态再生成）和缓存 revalidate 自调用的
>   服务绑定，**必须**指向当前 Worker 自身。两个字段保持一致即可避免 10143 错误。
> - **Cloudflare Workers Builds CI 的坑**：通过 Dashboard "Connect to Git" 部署时，
>   CI 系统会自动用 Dashboard 中创建的 Worker 名称覆盖 `name` 字段，但**不会**覆盖
>   `services[].service`。因此 `services[0].service` 必须预先硬编码为正确的 Worker 名称，
>   不能依赖 CI 自动同步。如果 CI 日志出现 "Failed to match Worker name" 警告紧接着
>   10143 错误，说明 `services[0].service` 与 Dashboard 中的 Worker 名称不一致——
>   修改 `web/wrangler.jsonc` 中两个字段为 Dashboard 显示的名称，提交后重新部署即可。
> - CLI 本地部署时也会读取 wrangler.jsonc，无需通过环境变量指定名称。

---

### 4.6 与 EdgeOne 部署的共存

GoKYCH 同时支持三种前端部署方式，互不干扰：

| 部署方式 | 构建命令 | 使用的配置 | 说明 |
|----------|----------|------------|------|
| **EdgeOne Makers**（默认推荐） | 平台自动 `npm run build` | 无特殊配置，平台自动检测 Next.js | 国内访问最快，自动 HTTPS/CDN |
| **Cloudflare Workers** | `npm run deploy` | `wrangler.jsonc` + `open-next.config.ts` | 海外访问快，免费额度有限 |
| **Docker standalone** | `STANDALONE=1 npm run build` | `next.config.ts` 中 `output:"standalone"` | VM 自托管，配合 nginx |

- `wrangler.jsonc`、`open-next.config.ts` 等文件对 EdgeOne 构建**无影响**
  （EdgeOne 使用自身 Next.js 适配器，不读取 Wrangler 配置）
- `next.config.ts` 中的 `initOpenNextCloudflareForDev()` 用 try/catch 包裹，
  未安装 `@opennextjs/cloudflare` 时静默跳过
- `public/_headers` 仅对 Cloudflare Pages 生效，EdgeOne/Docker 忽略

---

### 4.7 GitHub Actions 自动部署（可选）

如果你想每次 push 到 main 分支自动部署到 Cloudflare Workers：

1. 在 Cloudflare Dashboard → My Profile → API Tokens → Create Token
   - 选择「Edit Cloudflare Workers」模板
   - 权限至少包含：Account / Workers Scripts / Edit
   - 创建后复制 Token（只显示一次）

2. 在 GitHub 仓库 → Settings → Secrets and variables → Actions → New repository secret：
   - Name: `CLOUDFLARE_API_TOKEN`
   - Value: 刚才复制的 Token
   - 再添加一个：
   - Name: `CLOUDFLARE_ACCOUNT_ID`
   - Value: 你的 Cloudflare Account ID（在 Dashboard 右侧栏可找到）

3. 在仓库创建 `.github/workflows/deploy-cloudflare.yml`：

```yaml
name: Deploy to Cloudflare Workers

on:
  push:
    branches: [main]
    paths:
      - 'web/**'
      - '.github/workflows/deploy-cloudflare.yml'

jobs:
  deploy:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      - name: Deploy
        run: npm run cf:deploy
        env:
          CLOUDFLARE_API_TOKEN: ${{ secrets.CLOUDFLARE_API_TOKEN }}
          CLOUDFLARE_ACCOUNT_ID: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}
          NEXT_PUBLIC_API_BASE_URL: https://api.kych.net
```

> 注意：Worker 名称在 `web/wrangler.jsonc` 中配置，GitHub Actions 不通过环境变量指定名称。
> 如需自定义名称，先修改 wrangler.jsonc 并提交。

---

### 4.8 常见错误排查

| 错误信息 | 原因 | 解决方法 |
|----------|------|----------|
| `Service binding 'WORKER_SELF_REFERENCE' references Worker 'xxx' which was not found [code: 10143]` | 三种可能：(1) wrangler.jsonc 的 name 与 services[0].service 不一致；(2) services[0].service 与 Cloudflare Dashboard 中的 Worker 名称不匹配（Cloudflare CI 只覆盖 name 不覆盖 services[].service）；(3) 控制台存在同名但不同类型的 Worker | 确保 `web/wrangler.jsonc` 中 `"name"` 和 `"services[0].service"` 两个字段值完全相同，且与 Dashboard 中 Worker 名称一致；若 CI 日志有 "Failed to match Worker name" 警告，说明名称被 CI 覆盖但 services 未同步，以警告中的期望名称为准修改两个字段后提交重新部署 |
| `Error: Workers Paid plan is required` | 免费计划不支持 Git 构建（Dashboard方式）或 Worker 体积超限 | 升级到 Workers Paid（$5/月）；CLI方式免费计划通常够用（gzip约600KB-2MB，在3MiB限制内） |
| `Could not resolve "@opennextjs/cloudflare"` 或 `Cannot find module '@opennextjs/cloudflare'` | 构建依赖在 devDependencies 中，云端生产构建（NODE_ENV=production）时 npm 不安装 devDependencies；或使用 npx 导致网络问题/版本不一致 | 已修复：`@opennextjs/cloudflare` 和 `wrangler` 已移至 `dependencies`；确保 Build command 使用 `npm run cf:build`（本地依赖）而非 `npx @opennextjs/cloudflare build` |
| Dashboard构建失败：`ENOENT: no such file or directory, open 'wrangler.jsonc'` | Root directory 设错了（设为仓库根目录而非web/） | Dashboard → Worker → Settings → Builds → Root directory 改为 `web`，重新部署 |
| Dashboard构建失败：`npm ERR! enoent Could not read package.json` | 同上，根目录设错 | 同上，Root directory 必须设为 `web` |
| Dashboard构建失败：`Unknown command: deploy` | Build command 写了 `npm run cf:deploy`，导致在构建阶段就尝试wrangler deploy但没有API token | Build command 只写 `npm run cf:build`，部署由Cloudflare自动完成（Deploy command留空） |
| Dashboard部署成功但页面显示"文章不存在" | `NEXT_PUBLIC_API_BASE_URL` 未在**构建时**环境变量中设置（只设了运行时变量或用了secret） | Dashboard → Worker → Settings → Variables and Secrets → 在 **Environment Variables**（不是Secrets）中添加 `NEXT_PUBLIC_API_BASE_URL`，且确保在Build阶段可见；然后在Deployments中触发重新部署 |
| CLI部署成功但页面显示"文章不存在" | `NEXT_PUBLIC_API_BASE_URL` 未在构建时设置，SSR fetch 打到 `localhost:8000` | 检查 `web/.env.local` 是否存在且正确，重新 `npm run cf:deploy` |
| 登录后刷新显示未登录 | CORS 未配置 Workers 域名，Set-Cookie 被浏览器拦截 | 后端 `.env` 的 `CORS_ALLOWED_ORIGINS` 添加 Workers 域名，重启后端 |
| `Authentication Error: Invalid API token` | wrangler login 过期或 token 无效（仅CLI） | 重新运行 `npx wrangler login` |
| `A binding for ASSETS was not found` | `.open-next/assets/` 目录不存在或构建失败 | CLI：先运行 `npm run cf:build` 确认构建成功，再 `npm run cf:deploy`；Dashboard：查看Build logs确认构建步骤是否成功完成 |
| 静态资源（CSS/JS）404 | 构建产物不完整 | CLI：删除 `.open-next/` 目录后重新构建 `rm -rf .open-next && npm run cf:build`；Dashboard：在Deployments中触发Redeploy |
| Dashboard每次push都不自动部署 | 生产分支名设置错误（默认main，但你的分支可能是master） | Dashboard → Worker → Settings → Builds → Production branch 改为你的实际主分支名 |

---

## 5. DNS

| 记录 | 名称              | 类型  | 值                              |
|------|-------------------|-------|---------------------------------|
| A    | `kych.net`        | A     | `<VM 公网 IP>`                  |
| AAAA | `kych.net`        | AAAA  | `<VM IPv6>`（如有）             |
| A    | `api.kych.net`    | A     | `<VM 公网 IP>`                  |
| AAAA | `api.kych.net`    | AAAA  | `<VM IPv6>`（如有）             |
| CNAME| `eo.kych.net`     | CNAME | EdgeOne Makers 控制台给的 `<xxx>.maker.edgeone.app` 形式 |

> `kych.net` 和 `api.kych.net` 直连 VM（A 记录），certbot 在 VM
> 上签它们的 Let's Encrypt 证书。`eo.kych.net` 走 EdgeOne
> （CNAME），EdgeOne 自动签发 / 管理边缘 HTTPS 证书。

---

## 6. 端到端验证清单

部署完后按这个顺序过一遍：

1. **后端起得来**：`curl -fsS https://api.kych.net/healthz` →
   `{"db":"ok","status":"ok"}`
2. **前端 SSR 跑得动**：浏览器打开 `https://eo.kych.net/` →
   首页能渲染（HTML 里能看到文章列表/导航，不是一堆 `<script>` 等 JS）
3. **跨域带 cookie**：浏览器 devtools 看到 `https://eo.kych.net` 上
   发的 fetch 目标都是 `https://api.kych.net/api/...`，Response headers
   里有 `Access-Control-Allow-Origin: https://eo.kych.net` 和
   `Access-Control-Allow-Credentials: true`
4. **登录后台**：浏览器开 `https://eo.kych.net/admin`，登录 admin
5. **创建文章**：创建一个 md 文章，看列表里出现，前台 `/md/<slug>` 能访问
6. **Passkey 登录**（最严苛）：开无痕窗口 → `/auth/login` →
   「使用 Passkey 登录」→ 浏览器弹出 authenticator → 登录成功 →
   URL 跳到 `/admin`
7. **评论、行评论、评分** 各发一条；admin 后台能看到通知
8. **文件管理**：上传一张图 → 复制「URL」列 → 新窗口打开能直接看到
9. **`journalctl -u gokych`** 没有 ERROR / WARN（除了首次启动的
   "session secret" 等已知一次性日志）
10. **EdgeOne 控制台**：部署状态绿，无 build error；自定义域名
    `eo.kych.net` 显示"已签发证书" + "已激活"

Passkey 特别说明：浏览器弹的认证框子里写的是 `localhost` 是因为
RPID 用了 `localhost` — 这只在 `APP_DOMAIN=localhost:3000` 时发生。
生产 `APP_DOMAIN=https://eo.kych.net` 会弹出来是
`eo.kych.net`，完全正确。**RPID 必须是 eTLD+1**（不能跨
主域），所以前端域名和后端域名必须是同一棵域树的子域（都挂在
`kych.net` 下）。

---

## 7. 监控（够用就行）

最小集：

- **uptime**：EdgeOne 控制台自带"实时监控"（请求量 / 状态码 / 边缘
  健康），配 `https://eo.kych.net/` 与 `https://api.kych.net/api/health`
  两个 URL 探测；也可叠加云监控 / UptimeRobot 等外部探针
- **日志**：后端写 stdout → journald，配一个 rsyslog → 远程 syslog
  （papertrail/betterstack 都行）或者直接 `journalctl --since today -u gokych`
  每周翻一次。EdgeOne 边缘日志在控制台"日志"标签，按需查
- **磁盘**：`/opt/gokych/data` 涨得最厉害的就是 `uploads/`，加个
  简单的 `df` 检查到 cron，>80% 告警
- **MySQL**：mysqldump 失败 → 邮件告警（cron + mailx 拼一下）

---

## 8. 已知坑（deploy 之前先扫一眼）

| 坑 | 说明 | 规避 |
|----|------|------|
| Passkey 需要 HTTPS | WebAuthn 协议硬性要求 secure context | EdgeOne 自动 HTTPS（边缘节点）；VM 后端通过 certbot 拿证书 |
| RPID 域要匹配 | RPID 必须是 eTLD+1，且必须等于 `APP_DOMAIN` 解析出的 host | 前/后端都用 `*.kych.net` 子域 |
| Session cookie `Secure` | release 模式自动开 Secure，开发用 HTTP 测 cookie 不会带 | `.env` 一定设 `GIN_MODE=release` |
| MySQL `caching_sha2_password` | Go driver 默认支持，但需要 MySQL 8.0+ | 已在 docker-compose 锁定 8.0 |
| 时间戳时区 | `created_at` 走 MySQL server time；用 UTC | `data/` 里 `settings.yml` 改 timezone 或忽略（展示走浏览器 locale） |
| Uploads 体积 | nginx 默认 `client_max_body_size 1m` | 上面 nginx 配置改成 `50m` |
| 数据目录权限 | systemd `ProtectSystem=strict` 不给 /opt/gokych/data 写权限就起不来 | 配 `ReadWritePaths=/opt/gokych/data`（已加） |
| 跨域 cookie | CORS + `credentials: "include"` 不允许 `*` 源 | 后端用 `CORS_ALLOWED_ORIGINS` 显式列 |
| 上传 URL 相对路径 | 跨域下浏览器 404 | 已修：后端用 `PUBLIC_URL` 拼绝对路径（commit `90498db`），前端 `f.url` 直接用 |
| Passkey 突然 503 | `APP_DOMAIN` 改错 / RPID 跟前端 host 不匹配 | `journalctl -u gokych \| grep passkey` 看 startup log；RPID 必须是 eTLD+1 |
| EdgeOne 没拿到 `NEXT_PUBLIC_API_BASE_URL` | 浏览器 fetch 走相对路径 → 命中 eo.kych.net/api/... → 边缘没有这个路径 → 404 | 控制台"环境变量"必须设；构建后才能改，需要重新触发部署 |
| EdgeOne 上 `output: "standalone"` 干扰 | standalone 输出模式与 Makers 自带构建器冲突 | `web/next.config.ts` 已改成条件 opt-in（`STANDALONE=1` 才开），Makers 不设此环境变量，零冲突 |
| EdgeOne 改了构建器默认行为 | 平台升级可能改变默认 Next.js 构建参数 | 锁定仓库 `package.json` 里 `next` 版本；如遇问题在控制台"构建设置"显式指定 |
| CORS preflight 走错中间件 | OPTIONS 请求如果被 CSRF 中间件拦了，浏览器永远收不到 204 → 实际 mutation 也发不出去 | CORS 已在 `csrfMiddleware` 之前安装，preflight 直接 204 短路（commit `90498db`） |
| Cloudflare WORKER_SELF_REFERENCE 错误 10143 | 三种原因：(1) wrangler.jsonc 中 `name` 和 `services[0].service` 字段值不一致；(2) services[0].service 与 Dashboard 中 Worker 名称不匹配（Cloudflare Workers Builds CI 只覆盖 name 不覆盖 services[].service）；(3) 首次 Dashboard Git 连接部署时 wrangler.jsonc 的 name 与 CI 期望名称不一致 | 确保两个字段值完全相同且与 Dashboard Worker 名称一致；CI 日志中的 "Failed to match Worker name" 警告会显示 CI 期望的名称，以该名称为准修改两个字段，提交后重新部署 |
| Cloudflare 构建 `Could not resolve "@opennextjs/cloudflare"` | 构建依赖在 devDependencies 中，NODE_ENV=production 时 npm 不安装；或使用 npx 临时下载导致网络问题/版本不一致 | 已修复：`@opennextjs/cloudflare` 和 `wrangler` 在 `dependencies` 中；Dashboard Build command 使用 `npm run cf:build` |
| Cloudflare Workers 跨域失败 | 后端 `CORS_ALLOWED_ORIGINS` 未包含 workers.dev 域名或自定义域名 | 后端 `.env` 的 `CORS_ALLOWED_ORIGINS` 添加 Cloudflare 域名，逗号分隔 |
| Cloudflare 构建后 API 请求仍指向 localhost | `NEXT_PUBLIC_API_BASE_URL` 未在构建时设置（这是构建时变量，不是运行时变量） | 创建 `web/.env.local` 写入 `NEXT_PUBLIC_API_BASE_URL=https://api.example.com`，或构建时通过 shell 环境变量传入；不要用 `wrangler secret put` 设置 NEXT_PUBLIC_* 变量 |

---

## 9. 回滚预案

| 场景                    | 怎么办                                              |
|-------------------------|------------------------------------------------------|
| 后端起不来              | `sudo systemctl restart gokych`；不行就 `cp gokych.prev gokych` 然后 restart |
| Passkey 突然挂          | 看 `journalctl -u gokych \| grep passkey`；`APP_DOMAIN` 改错就 503 |
| 前端某次发布挂了        | EdgeOne Makers 控制台 → 部署历史 → 上一条 → 回滚（30 秒内全球生效） |
| 前端 build 持续失败     | 看 Makers 控制台 build log，定位错误；`output: "standalone"` 误开就删 commit |
| 数据库升级挂了          | `mv /var/backups/gokych/db-latest.sql.gz /tmp/`，从备份恢复（drop + create + 灌入） |
| 整个翻车                | 重新跑 §2 整套 + EdgeOne 控制台重连；10 分钟内能恢复 |

---

## 10. 一行话总结

> **Ubuntu 24 VM 上跑 gokych 单实例（systemd + nginx + MySQL 8），
> 前端 Next.js 部署在腾讯云 EdgeOne Makers 边缘（自动构建 + 自动
> HTTPS + 自动 CNAME）。浏览器 / SSR 都跨域 fetch `api.kych.net`，
> 由后端 `CORS_ALLOWED_ORIGINS` + `PUBLIC_URL` 兜底。`.env` 里
> `CORS_ALLOWED_ORIGINS` / `PUBLIC_URL` / `APP_DOMAIN` 这三个是
> 生产正确性的关键 — 改完 `.env` 必须 `systemctl restart gokych`；
> EdgeOne 控制台环境变量改了必须重新触发部署。**

### 一键部署（macOS / Linux 都能跑）

仓库里有两条脚本，覆盖"打 release"和"装 release"两件事：

**`scripts/build-release.sh`** — 维护者打 release 时跑。打 4 个平台
二进制到 `dist/`，生成 `SHA256SUMS`，可选 `--upload` 直接 `gh release create`。
版本号通过命令行输入（`vX.Y.Z` 或 `X.Y.Z`，可带 `-rc1` 等后缀）：

```bash
./scripts/build-release.sh v0.1.0                       # 位置参数
./scripts/build-release.sh --version v0.1.0 --upload    # 长 flag + 直接上传
./scripts/build-release.sh -v 0.2.0-rc1                 # 短 flag + pre-release
./scripts/build-release.sh                              # 不指定 → git describe
VERSION=v0.1.0 ./scripts/build-release.sh               # 环境变量（兼容旧用法）
```

`--upload` 禁止 `dev` / `*-dirty` 之类的非正式版本号。

**`scripts/install-backend.sh`** — 任何机器（包括目标 Ubuntu VM 自身）
从 GitHub / GitCode Release 拉对应平台的二进制，校验 hash，装到
`/usr/local/bin/gokych`：

> **必须从 GitHub raw 拉脚本。** `gitcode.com` 不暴露 raw HTTP 文件 URL
> （任何 `/raw/...` 路径都返回 AtomGit 落地页 HTML），所以 `curl | bash`
> 一键装这条路**只走 GitHub raw**。如果你在国内连 GitHub 慢，本地先
> `git clone https://gitcode.com/CrossDark/GoKych.git` 再跑脚本，或者让
> EdgeOne Makers 反代加速（不影响 `curl` 那行）。
>
> **`GOKYCH_HOST=gitcode` 只控制脚本内部拉二进制的源**（走 gitcode 的
> release API），不影响 `curl` 那行的 URL。所以下面 4 个例子里，前 3 个
> 走 GitHub raw 装脚本、二进制也跟着从 GitHub Release 拉；第 4 个是先
> `git clone`（绕开 gitcode 没 raw 的问题），二进制切到 gitcode。

```bash
# 目标 VM 上跑：装到 /usr/local/bin（要 sudo） — 脚本 + 二进制都走 GitHub
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | sudo bash

# 装到用户目录（不要 sudo）
PREFIX=$HOME/.local curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | bash

# 装特定版本
GOKYCH_VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | bash

# 想要二进制从 gitcode 拉：先 clone（绕开 gitcode 没 raw），再传 GOKYCH_HOST
git clone https://gitcode.com/CrossDark/GoKych.git /tmp/gokych
GOKYCH_HOST=gitcode sudo bash /tmp/gokych/scripts/install-backend.sh
```

**`scripts/install-all.sh`** — **新 VM 首次部署的首选**。在目标
Ubuntu VM 上 `curl | sudo bash` 跑一次，自动完成 *所有* 后端初始化
（系统包 → 二进制下载+校验 → MySQL 建库建用户 → 写 .env
随机密钥 → systemd 单元 → nginx HTTP-only 配置 → certbot 签证书
→ 健康检查 → 写 MySQL 备份脚本），**不负责前端** —— 前端仍然
走 EdgeOne Makers 自动构建。脚本跑完会打印前端需要的环境变量和
DNS 解析记录。

> `install-all.sh` = `install-backend.sh`（装二进制）+ `deploy-backend.sh`
> （初始化）合并的"一站式"版本，区别是它设计成在 VM 本机跑、零依赖
> （不需要 Go、不需要 SSH、不需要本地克隆仓库 —— 二进制从
> GitHub/GitCode Release 拉）。**只支持 Ubuntu 22.04/24.04**。
> 详见 `--help`。

```bash
# 在全新的 Ubuntu VM 上：先 DNS 把 api.kych.net / kych.net 指过来，
# 然后一行搞定（--yes 跳过确认；生产建议去掉，自己看一遍再回车）
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-all.sh | \
  sudo bash -s -- \
    --site-name "我的网站" \
    --api-domain "api.kych.net" \
    --main-domain "kych.net" \
    --frontend-domain "eo.kych.net" \
    --email "admin@example.com" \
    --admin-password "ChangeMe-Strong-Pwd" \
    --yes

# 装特定版本（默认 GitHub latest）
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-all.sh | \
  sudo bash -s -- --release v0.2.0 --yes

# 二进制从 gitcode 拉（先 clone 绕开 gitcode 没 raw 的问题）
git clone https://gitcode.com/CrossDark/GoKych.git /tmp/gokych
sudo bash /tmp/gokych/scripts/install-all.sh --host gitcode --yes

# 卸载（保留 /var/lib/mysql；想清干净加 --purge-data）
sudo bash /tmp/gokych/scripts/install-all.sh --uninstall
sudo bash /tmp/gokych/scripts/install-all.sh --uninstall --purge-data
```

**`scripts/deploy-backend.sh`** — 跨平台编译后推到目标 VM，初始化
systemd / nginx / MySQL / TLS（首次部署 + `--update` 后续更新都支持）。
**不负责前端** —— 前端走 EdgeOne Makers 自动构建，跟本脚本无关。

> **跑在哪？** 两种模式都支持,**自动检测**(也可用 `LOCAL_MODE=1` 强制):
>
> 1. **远端模式(默认)** — 操作机(Mac/Linux)跑,`go build` 出二进制,
>    `ssh`/`scp` 推到 VM。需要免密 SSH 到 `REMOTE_USER@REMOTE_HOST`。
> 2. **本机模式** — 直接在 Ubuntu VM 上跑(`sudo bash`),不走 SSH。
>    默认需要 Go 编译器(脚本会 `go build`);如果 VM 上已经用
>    `install-backend.sh` 装好了 `gokych`,加 `--use-installed` 直接复用,
>    VM 不需要 Go(只要能 clone 仓库 + 跑 apt/systemctl 等)。
>
> 触发本机模式:`LOCAL_MODE=1` 环境变量 / `REMOTE_HOST` 留空 /
> `REMOTE_HOST` 是 `localhost`/`127.0.0.1` / `REMOTE_HOST` 匹配本机
> hostname / `REMOTE_HOST` 解析到 127.0.0.0/8。
>
> **不能用 `curl | bash` 一键跑。** 默认行为需要 Go 源码(已装的话用
> `--use-installed` 也行)。如果只想要"在 VM 上装/更新二进制",
> 用 `install-backend.sh` — 那个才是设计成 `curl ... | sudo bash` 的
> (拉 GitHub/GitCode Release 预编译产物 + 校验 sha256)。本节后面有
> 它的用法。

```bash
# 首次部署：自动建用户、装包、初始化 MySQL、写 systemd、写 nginx(HTTP-only)、
#           certbot --nginx 签 TLS（2 个域名：api.kych.net / kych.net；
#           eo.kych.net 由 EdgeOne 自动签，VM 不管）
./scripts/deploy-backend.sh

# 后续更新：只重传二进制 + 重启（.env 里的密钥自动从远端读回保留）
./scripts/deploy-backend.sh --update

# ARM64 Ubuntu VM（AWS Graviton / 树莓派）
GOARCH=arm64 ./scripts/deploy-backend.sh
```

四个脚本的职责切分：
- `build-release.sh`    → 多平台二进制 + 哈希
- `install-backend.sh`  → 单机装（适合 macOS 本地、临时测试、容器）
- `install-all.sh`      → **Ubuntu VM 一键完整后端初始化（推荐）**
- `deploy-backend.sh`   → 远程 VM 后端整套（适合生产；操作机驱动）

**前端发布走 EdgeOne Makers，没有专门的 deploy 脚本 —— `git push main`
就够了。**

---

## 11. 缓存优化：CF 代理 + ISR + 按需失效（空间换时间）

针对"CF Workers / EdgeOne 前端 + 海外后端"对大陆用户慢的问题，做了
三层缓存优化。核心思路：**用缓存换时间、节约后端**——让大陆用户尽量
命中离自己最近的边缘缓存，少回源到海外。

### 11.1 api.ywda.net 接入 Cloudflare 代理（运维）

- DNS 把 `api.ywda.net` 从灰云改**橙云**（Proxied）。浏览器与两端 SSR
  的 API 请求打到离自己最近的 CF PoP，再走 CF 骨干回源，比 ISP 直连
  海外稳定得多；公开响应还能被边缘缓存。
- SSL/TLS 模式 = **Full (strict)**（用 nginx 上 certbot 的现有证书）。
  Flexible 会让 cookie/WebAuthn 失效。
- **Cache Rule**（Caching → Cache Rules）：`Hostname eq "api.ywda.net"
  and URI Path starts with "/api/"` → Eligible for cache / Edge TTL =
  Respect origin TTL。**不要选 "Cache everything"**，否则会把
  `/api/auth/me`（无 `Cache-Control`）也缓存，跨用户串数据。后端已对
  公开端点设 `public, max-age=…`、对登录态设 `private`，"Respect
  origin" 会正确区分。
- **nginx 恢复真实 IP**（否则后端限流看到的都是 CF IP）：
  `include /etc/nginx/snippets/cloudflare-realip.conf;`（仓库
  `scripts/nginx-cloudflare-realip.conf`，含 CF 全部 v4/v6 段 +
  `real_ip_header CF-Connecting-IP`）。Go 侧 `TRUSTED_PROXIES=127.0.0.1`
  不变（Go 只信 nginx，nginx 现在信 CF）。

  > **新部署**：`scripts/deploy-backend.sh` 与 `scripts/install-all.sh`
  > 已自动把 snippet 写到 `/etc/nginx/snippets/cloudflare-realip.conf`
  > 并在 `api` server 块里 `include`，无需手动改。
  >
  > **已存在的 VM** 手动补一次（snippet 仓库里没有就先拷过去）：
  > ```bash
  > sudo mkdir -p /etc/nginx/snippets
  > sudo cp scripts/nginx-cloudflare-realip.conf /etc/nginx/snippets/cloudflare-realip.conf
  > # 在 /etc/nginx/sites-available/gokych 的 api server 块里、location 之前加：
  > #     include /etc/nginx/snippets/cloudflare-realip.conf;
  > sudo nginx -t && sudo systemctl reload nginx
  > ```

### 11.2 解锁边缘 HTML 缓存（代码：去 nonce）

之前 `app/layout.tsx` 调 `await headers()` 取每请求 CSP nonce，**这
把所有路由强制动态渲染、禁用了 Next.js Full Route Cache**——EdgeOne
和 CF Workers 都没法缓存 SSR HTML。现已：

- 删 `web/middleware.ts`（移除每请求 nonce 生成）。
- `web/next.config.ts` 加静态 CSP（`script-src 'self' 'unsafe-inline'`，
  含 `NEXT_PUBLIC_API_BASE_URL` 的 origin 到 `connect-src`/`img-src`/
  `style-src`/`media-src`，避免阻断跨域 API / 外部图片 / YouTube 嵌入），
  并由 `headers()` 注入——静态头会被烘焙进缓存响应，比 middleware 更
  可靠。
- `app/layout.tsx` 删 `headers()`/nonce。

效果：所有 `export const revalidate` 重新生效，EdgeOne 大陆节点缓存
HTML（命中即零 SSR、零回源），CF Workers 区域缓存。

### 11.3 拉长缓存窗口（代码：前端 revalidate + 后端 Cache-Control）

前端 `revalidate`：文章详情 300→**1800**、列表/首页 60→**300**、
site/labels/themes 60→**600**；`web/lib/api/client.ts` 默认 60→**300**。
后端 `Cache-Control` 同步调长（articles 1800、site 600、home 300、
labels 600、themes 600），均保留大 `stale-while-revalidate`。

### 11.4 按需失效 webhook（长缓存下编辑仍秒级刷新）

长缓存会让编辑/评论最多延迟一个窗口才对他人可见。加了 webhook：

- 前端 `web/app/revalidate/route.ts`（**在 `/revalidate`，不在 `/api/`**，
  避开 dev 的 `/api/*` 代理）：校验 `Authorization: Bearer $REVALIDATE_SECRET`
  后调 `revalidateTag`/`revalidatePath`。两端都生效（CF 侧靠
  `WORKER_SELF_REFERENCE` 自调用）。
- 后端 `internal/api/revalidate.go`：`s.revalidateFrontend(tags, paths)`
  fire-and-forget 回调 `FRONTEND_REVALIDATE_URLS` 里每个前端。已在
  文章 CRUD、评论/行评论、评分增删、标签 CRUD、通知 CRUD、设置更新、
  子站点增删、推荐增删后接入。文章变更失效 `articles`+`home`+
  `article:type:slug` + 对应 path；评论/评分失效单文章；标签失效
  `labels`+`/labels`；设置/子站点失效 `site`+`home`。
- 环境变量（前后端共享密钥，`openssl rand -hex 32`）：
  - 后端 `.env`：`FRONTEND_REVALIDATE_URLS=https://eo.ywda.net/revalidate,https://cf.ywda.net/revalidate` + `REVALIDATE_SECRET=…`
  - 前端：CF Workers secret / EdgeOne 环境变量 `REVALIDATE_SECRET=…`

### 11.5 削减客户端请求瀑布（代码：AppProviders）

`web/components/AppData.tsx`（server）SSR 取 site/labels/themes（均
`anon`+缓存，不碰 `cookies()`，ISR 友好）→ `AppProviders.tsx`（client）
单次 `getMe`+`getCsrf` 共享给 Header/ThemeStylesheet/ArticleView/
RatingWidget/CommentSection。文章页客户端请求 5–8 → 匿名 1 / 登录 2，
且 `getMe→getCsrf` 串行链从 4 条降到 1 条。登录态在匿名缓存 HTML 上
靠 context 乐观显示编辑链接（权限仍由编辑页后端校验）。

### 11.6 验证

```bash
curl -I https://eo.ywda.net/wikidot/电梯生存须知   # 二次请求见 x-cache/age 命中
curl -I https://api.ywda.net/api/site              # CF-Cache-Status: HIT
curl -I https://api.ywda.net/api/auth/me           # 应为 DYNAMIC（不缓存）
# 后台改一篇文章后数秒内 cf.ywda.net / eo.ywda.net 均刷新
```

---

## 附录 A：相关 commit 一览（按时间倒序）

| commit    | 主题                                                         |
|-----------|--------------------------------------------------------------|
| *new*     | fix(cloudflare): Worker 名称统一为 creater-rule-web，修复 CI 10143 错误 |
| *new*     | feat(admin): allow_all_edit 站长专属开关（所有用户编辑所有文章） |
| *new*     | feat(deploy): scripts/install-all.sh — Ubuntu 一键后端初始化 (curl\|sudo bash) |
| `0baf836` | docs(deploy): clarify deploy-backend.sh runs on operator machine, not on the VM |
| `9441bee` | docs(deploy): rewrite deployment.md for EdgeOne Makers; tick done items in TODO |
| `59cbe67` | chore(deploy): drop deploy-frontend.sh + trim backend deploy for EdgeOne Makers |
| `89dfc61` | chore(deploy): next.config.ts — `output: standalone` is now STANDALONE=1 opt-in |
| `e7d3bd5` | feat(deploy): split apiUrl() helper — every cross-origin fetch goes through it |
| `b8c8387` | fix(test): TestBuildWebAuthnOrigins — dedupe + compare as set |
| `dccaf65` | docs: web/lib/api.ts use NEXT_PUBLIC_API_BASE_URL (跨域前置) |
| `61a5a73` | docs: first pass at EdgeOne Makers deployment plan |
| `90498db` | feat(deploy): CORS middleware + absolute PUBLIC_URL          |
| `192cbb9` | docs: deployment plan for Ubuntu 24 + Cloudflare Pages (已废弃，被本文件 EdgeOne 方案取代) |
| `1db5623` | docs(env): document APP_DOMAIN in .env.example              |
| `cef28c9` | docs: add TODO + integration/profile/unicode test scripts    |
| `aed9200` | feat(article): regular users can CRUD their own articles    |
| `7154585` | fix(auth): persist BackupEligible + require discoverable     |

`90498db` 是把"跨域可工作"这件事从方案层面落到代码的 PR — 部署前
请确认仓库 ≥ 这个 commit，否则需要先把缺的代码补上。`scripts/deploy-backend.sh`
是后续加的，把 §2 的手抄步骤封装成一条命令。

---

## 附录 B：本方案落地涉及的代码改动

把前端从"VM 跑 standalone + EdgeOne CDN 回源"切到"EdgeOne Makers
边缘 SSR"对代码的改动集中在两个文件 + 一处脚本：

### B.1 `web/lib/api.ts` —— 拆出 `apiUrl()` helper

之前浏览器 fetch 走相对路径 `/api/...`，依赖 `next.config.ts` 的
dev rewrites。EdgeOne Makers 模式下前端跑在 `eo.kych.net`，相对路径
会落到 EdgeOne 边缘没有的路径上 → 404。

修法：把 `BASE` 解析集中到一个 helper：

```ts
const BASE =
  process.env.NEXT_PUBLIC_API_BASE_URL ||
  (typeof window === "undefined" ? process.env.API_BASE_URL || "http://localhost:8000" : "");

export function apiUrl(path: string): string {
  if (!path.startsWith("/")) path = "/" + path;
  return `${BASE}${path}`;
}
```

所有 fetch + href（下载链接、CSS link）都改成 `fetch(apiUrl(...))` /
`href={apiUrl(...)}`。BASE 在生产 = `NEXT_PUBLIC_API_BASE_URL` =
`https://api.kych.net`，绝对跨域 fetch；在 dev = `""`（浏览器侧），
退化为相对路径，被 next dev rewrites 接管。

覆盖文件：
- `web/lib/api.ts`（自身导出 + 上传 helper）
- `web/app/admin/settings/page.tsx`
- `web/app/admin/passkeys/page.tsx`
- `web/app/admin/profile/page.tsx`
- `web/app/auth/login/page.tsx`
- `web/app/admin/api-keys/page.tsx`
- `web/components/ArticleView.tsx`（PDF 下载 href）
- `web/components/ThemeStylesheet.tsx`（主题 CSS link）

### B.2 `web/next.config.ts` —— standalone 改成条件 opt-in

EdgeOne Makers 自带 Next.js 构建器有自己的输出模式；强制 `output:
"standalone"` 会改变构建产物的形态，可能跟 Makers 适配器冲突。
但 `web/Dockerfile` 的 fallback 路径仍然想要 standalone 模式（VM /
容器自托管 Node 时）。

```ts
...(process.env.STANDALONE === "1" ? { output: "standalone" as const } : {}),
```

EdgeOne Makers 构建管线不设 `STANDALONE` 环境变量，零冲突；VM /
docker-compose / CI 自托管设 `STANDALONE=1` 仍能拿到 standalone
输出。

### B.3 `scripts/deploy-frontend.sh` —— 删除

EdgeOne Makers 接管前端发布，`git push main` 触发自动构建。VM 上
不再需要 Node / `next-server` systemd / standalone tar 上传。脚本删了。

`scripts/deploy-backend.sh` 也对应简化：nginx 只配 `api.kych.net` +
`kych.net` 两个 server 块（不再配 eo），certbot 只签这俩域（eo 由
EdgeOne 平台托管）。

### B.4 后端代码不动

后端的 CORS / `PUBLIC_URL` / `APP_DOMAIN` 已经在 `90498db` PR 里
完整实现；本方案完全沿用。`cmd/gokych/main.go:49` 的
`SESSION_SECRET` 默认值护栏、webauthn origin 推导、session 中间件
的 cookie `Secure` flag 都不需要动。