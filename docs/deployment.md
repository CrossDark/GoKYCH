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
   - `NEXT_PUBLIC_API_BASE_URL` = `https://api.kych.net`
   - 其他可选：`API_BASE_URL` = `https://api.kych.net`（SSR 路径
     备用，`apiUrl()` 已经优先读 `NEXT_PUBLIC_*`）
3. **构建命令 / 输出目录**：留平台默认（`next build`，输出到
   `.next/`）。**不要**手动指定 `output: "standalone"` — 我们仓库
   里 `web/next.config.ts` 已经把它改成条件 opt-in（仅当
   `STANDALONE=1` 才开），Makers 不会设这个环境变量，平台用自己
   默认的 SSR 模式构建。
4. **自定义域名**：添加 `eo.kych.net`。EdgeOne 自动签发 / 续期
   该域名的边缘 HTTPS 证书，并在边缘绑好 CNAME 目标值。把这个
   CNAME 加到 DNS（见 §4）。
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

---

## 4. DNS

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

## 5. 端到端验证清单

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

## 6. 监控（够用就行）

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

## 7. 已知坑（deploy 之前先扫一眼）

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

---

## 8. 回滚预案

| 场景                    | 怎么办                                              |
|-------------------------|------------------------------------------------------|
| 后端起不来              | `sudo systemctl restart gokych`；不行就 `cp gokych.prev gokych` 然后 restart |
| Passkey 突然挂          | 看 `journalctl -u gokych \| grep passkey`；`APP_DOMAIN` 改错就 503 |
| 前端某次发布挂了        | EdgeOne Makers 控制台 → 部署历史 → 上一条 → 回滚（30 秒内全球生效） |
| 前端 build 持续失败     | 看 Makers 控制台 build log，定位错误；`output: "standalone"` 误开就删 commit |
| 数据库升级挂了          | `mv /var/backups/gokych/db-latest.sql.gz /tmp/`，从备份恢复（drop + create + 灌入） |
| 整个翻车                | 重新跑 §2 整套 + EdgeOne 控制台重连；10 分钟内能恢复 |

---

## 9. 一行话总结

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
二进制到 `dist/`，生成 `SHA256SUMS`，可选 `--upload` 直接 `gh release create`：

```bash
VERSION=v0.1.0 ./scripts/build-release.sh
VERSION=v0.1.0 ./scripts/build-release.sh --upload   # 自动创建 GitHub Release
```

**`scripts/install-backend.sh`** — 任何机器（包括目标 Ubuntu VM 自身）
从 GitHub / GitCode Release 拉对应平台的二进制，校验 hash，装到
`/usr/local/bin/gokych`：

```bash
# 目标 VM 上跑：装到 /usr/local/bin（要 sudo）
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | sudo bash

# 装到用户目录（不要 sudo）
PREFIX=$HOME/.local curl ... | bash

# 装特定版本
GOKYCH_VERSION=v0.1.0 curl ... | bash

# 强制走 GitCode（GitHub 在国内慢）
GOKYCH_HOST=gitcode curl ... | bash
```

**`scripts/deploy-backend.sh`** — 跨平台编译后推到目标 VM，初始化
systemd / nginx / MySQL / TLS（首次部署 + `--update` 后续更新都支持）。
**不负责前端** —— 前端走 EdgeOne Makers 自动构建，跟本脚本无关。

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

三个脚本的职责切分：
- `build-release.sh`    → 多平台二进制 + 哈希
- `install-backend.sh`  → 单机装（适合 macOS 本地、临时测试、容器）
- `deploy-backend.sh`   → 远程 VM 后端整套（适合生产）

**前端发布走 EdgeOne Makers，没有专门的 deploy 脚本 —— `git push main`
就够了。**

---

## 附录 A：相关 commit 一览（按时间倒序）

| commit    | 主题                                                         |
|-----------|--------------------------------------------------------------|
| (pending) | chore: EdgeOne Makers 部署切线 — apiUrl() + standalone opt-in + docs 重写 |
| (pending) | chore: delete scripts/deploy-frontend.sh — EdgeOne Makers 接管前端 |
| (pending) | fix(test): TestBuildWebAuthnOrigins 期望顺序跟实现不一致，改用集合比较 |
| (pending) | chore: api.ts split apiUrl helper — BASE 解析集中到一处 |
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