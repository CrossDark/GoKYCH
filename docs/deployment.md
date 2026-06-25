# GoKYCH 部署方案 — Ubuntu 24 (后端) + 腾讯云 EdgeOne (前端 CDN/HTTPS)

> 范围：把 `main` 分支当前状态（≥ commit `90498db`，即 "feat(deploy):
> CORS middleware + absolute PUBLIC_URL for cross-origin frontends"
> 之后）以生产形态部署。后端单实例跑在 Ubuntu 24.04 LTS VM 上
> （systemd + nginx + MySQL 8），前端在同一台 VM 上跑 Next.js
> standalone `server.js`（systemd + nginx 反代），由腾讯云 EdgeOne
> 做 CDN / HTTPS 边缘加速，回源到 VM。前端代码零改动，保留全部 SSR。

> 之前的版本里把"补 CORS + PUBLIC_URL"列为 §0 的阻塞项 —
> 那个 PR（`90498db`）已经合到 main 了，本方案按"已就位"来写。
> 如果你的部署起点早于那个 commit，参考那个 commit 的 message
> 里的前置条件清单。

---

## 0. 一句话总结

后端 systemd 起一个 Go binary，nginx 在前面收 TLS；MySQL 在同机；
前端在同一 VM 上跑 Next.js standalone `server.js`（systemd `next-server`
+ nginx 反代 :3000），由腾讯云 EdgeOne 做 CDN/HTTPS 边缘节点回源。
`kych.net` 和 `api.kych.net` 直连 VM（A 记录），`eo.kych.net` 走
EdgeOne（CNAME）。前端 `eo.kych.net` 的 `/api/*` 由 nginx 同 origin
转发到 :8000，不触发 CORS。

---

## 1. 整体架构

```
                       ┌────────────────────────────────────────────┐
                       │            腾讯云 EdgeOne                   │
   用户 ────────────▶  │  eo.kych.net      (CDN/HTTPS 边缘)   │
                       │  api.kych.net         (CDN/HTTPS 边缘)   │
                       │      ↓ 边缘缓存 + 回源                     │
                       └────────────────┬───────────────────────────┘
                                        │
                           ┌────────────▼─────────────────────────────┐
                           │  Ubuntu 24.04 LTS VM                    │
                           │                                          │
                           │  :443 nginx (TLS via EdgeOne 回源证书   │
                           │               或本机 certbot，二选一)  │
                           │    ├─ eo.kych.net                 │
                           │    │     └─ /  ─▶ :3000 next-server     │
                           │    ├─ api.kych.net                    │
                           │    │     ├─ /api/*     ─▶ :8000 gokych   │
                           │    │     ├─ /uploads/* ─▶ :8000 gokych   │
                           │    │     └─ /avatars/* ─▶ :8000 gokych   │
                           │                                          │
                           │  :3306 mysqld (systemd)                 │
                           │                                          │
                           │  /opt/gokych/        (后端 binary+data) │
                           │  /opt/next-server/   (前端 standalone)  │
                           │  /var/backups/       (daily backup)     │
                           └──────────────────────────────────────────┘
```

域名分配（推荐）：
- `eo.kych.net` → EdgeOne → VM nginx `gokych` server 块 → :3000
- `api.kych.net` → EdgeOne → VM nginx `api` server 块 → :8000

两个域名都接入 EdgeOne（站点加速 + HTTPS），回源指向 VM 公网 IP。
API 都挂在 `api.kych.net` 下，前端 SSR 通过 `API_BASE_URL` 知道；
浏览器端 fetch 走相对路径（同 origin），EdgeOne 把 `eo.kych.net`
的 `/api/*` 透传回源到 VM。

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
# /avatars/* 路径前面，让前端（EdgeOne 边缘节点或浏览器）能直接
# fetch 图片（不走 Next.js rewrite——生产路径下 Next.js standalone
# server 没有跨域代理）。
# CORS_ALLOWED_ORIGINS：允许跨域 fetch 的 origin 列表，逗号分隔。
# 带 cookie 的请求不能用通配符 origin（CORS 规范），所以必须显式列。
# 多个 origin 写一起即可。dev 阶段经常要同时跑前/后端两个端口，
# 把 http://localhost:3000 加上；纯 prod 环境就只留前端域名。
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

三个域名三个 server 块，**全部只有 `listen 80`**：

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

# ─ eo.kych.net ─ 前端 SSR + API 顺路转发(同 origin, 不触发 CORS) ─
server {
    listen 80;
    server_name eo.kych.net;

    client_max_body_size 50m;

    # 浏览器 /api/* 请求同 origin 命中这里 → 转给后端 :8000
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
    # Next.js standalone server.js
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        "upgrade";
        proxy_read_timeout 60s;
    }
}

# ─ kych.net ─ 主域名 301 跳转到 EdgeOne 加速的前端 ─
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
#   2. 签发 3 个域名的证书
#   3. 自动往 nginx 配置里插入 listen 443 ssl + 证径路径
#   4. 自动给每个 listen 80 块加 return 301 → https
#   5. reload nginx
sudo certbot --nginx \
  -d api.kych.net -d eo.kych.net -d kych.net \
  --non-interactive --agree-tos -m you@kych.net --redirect
```

> **`--redirect`** 让 certbot 顺手给每个 80 块加 301 跳转到 443，
> 不用手动编辑。签好后 `nginx -t` 看一眼完整配置会有 6 个 server
> 块（3 个 80 跳转 + 3 个 443 ssl）。
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

## 3. 前端（EdgeOne 回源 + VM 跑 Next.js standalone）

### 3.1 形态选择说明

EdgeOne 同时支持「Pages 静态导出」和「边缘函数（V8 isolate）」两
种托管方式。本项目大量页面（首页、文章详情、admin）用了 Next.js
App Router 的 Server Components + `cookies()`，迁静态导出要逐页
改客户端 fetch、丢 SSR；迁边缘函数要逐个验证 `dompurify` 等 Node
依赖的兼容性。为了**零代码改动 + 保留 SSR/SEO**，这里选第三条路：
**在 VM 上跑 Next.js standalone `server.js`（systemd `next-server`），
EdgeOne 只做 CDN / HTTPS 边缘节点，回源到 VM 的 :443 nginx**。

代价是一个常驻 Node 进程（~80 MB RSS，远小于多一个 VM），换来的是
前端代码、`cookies()`、SSR 行为全部不动。

### 3.2 `web/next.config.ts`（已在仓库中）

```ts
output: "standalone",
// dev rewrites 保持原样——生产路径不跑 next dev，
// 跨域走 CORS + PUBLIC_URL。
```

`output: "standalone"` 让 `next build` 把用到的 `node_modules`
子树打包进 `.next/standalone/`，并生成 `.next/standalone/server.js`，
运行时只需要这一个目录 + `.next/static` + `public/`，不再需要仓库
根的 `node_modules`。Dockerfile 早就在按这个假设 COPY，但仓库实际
没设过——现已补上（commit 见附录 B）。

### 3.3 编译并发布到 VM

开发机一次性交叉编译并把 standalone 产物推到 VM：

```bash
# 在仓库根
cd web
npm ci
npm run build                                   # 产出 .next/standalone / .next/static

# 打包成可发布 tarball（只含运行时必需的目录）
tar -czf /tmp/next-server.tgz \
  -C .next/standalone . \
  -C .. .next/static \
  -C .. public

scp /tmp/next-server.tgz deploy@api.kych.net:/tmp/
ssh deploy@api.kych.net '
  set -e
  sudo mkdir -p /opt/next-server
  sudo rm -rf /opt/next-server/*
  sudo tar -xzf /tmp/next-server.tgz -C /opt/next-server
  sudo cp -r /opt/next-server/.next/static /opt/next-server/.next/static
  sudo cp -r /opt/next-server/public /opt/next-server/public 2>/dev/null || true
  sudo chown -R nextjs:nextjs /opt/next-server
  sudo systemctl restart next-server
  sleep 2
  curl -fsS http://127.0.0.1:3000/ -o /dev/null && echo "next-server ok"
'
```

> 也可以在 VM 上 `git clone && cd web && npm ci && npm run build`
> 就地构建，省掉 scp——和后端 `scripts/deploy-backend.sh` 的思路
> 一致，选哪种看团队习惯。**后续更新**复用同一脚本即可。

### 3.4 systemd unit `/etc/systemd/system/next-server.service`

```ini
[Unit]
Description=GoKYCH Next.js frontend (standalone server.js)
After=network.target gokych.service
Wants=gokych.service

[Service]
Type=simple
User=nextjs
Group=nextjs
WorkingDirectory=/opt/next-server
Environment=NODE_ENV=production
Environment=PORT=3000
Environment=HOSTNAME=127.0.0.1
Environment=API_BASE_URL=http://127.0.0.1:8000
ExecStart=/usr/bin/node /opt/next-server/server.js
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/next-server
PrivateTmp=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd --system --home-dir /opt/next-server --shell /usr/sbin/nologin nextjs
sudo systemctl daemon-reload
sudo systemctl enable --now next-server
sudo systemctl status next-server
sudo journalctl -u next-server -f          # 看到 "> Ready" 就行
```

> `API_BASE_URL=http://127.0.0.1:8000`：SSR 在同机回环调后端，
> 不走外网 / EdgeOne，最快也最稳。浏览器端 fetch 走相对路径，
> 命中 EdgeOne 的 `eo.kych.net/api/*` → 回源到 nginx → :8000。

### 3.5 nginx 站点 `/etc/nginx/sites-available/gokych-frontend`

新建第二个 server 块，专门接前端域名：

```nginx
# HTTP → HTTPS（EdgeOne 回源如果走 HTTPS，证书用 EdgeOne 下发
# 的"回源证书"或本机 certbot 任选其一；纯 HTTP 回源也行，但强烈
# 建议开启 EdgeOne 到源站的 HTTPS 回源，避免链路裸明文）。
server {
    listen 80;
    server_name eo.kych.net;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name eo.kych.net;

    # 方案 A：用本机 certbot 签的证书（域名归你管，EdgeOne 回源校验关）
    # 方案 B：用 EdgeOne 控制台"源站证书"下发的证书
    ssl_certificate     /etc/letsencrypt/live/eo.kych.net/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/eo.kych.net/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;

    client_max_body_size 50m;

    # Next.js standalone 是个常驻 server，反代到 :3000
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        # Next.js WebSocket（HMR，dev 才用；prod 留着无害）
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        "upgrade";
        proxy_read_timeout 60s;
    }

    # 浏览器端 /api/*、/uploads/*、/avatars/* 与前端同 origin，
    # nginx 直接把它们转给后端 :8000，省得跨域跳到 api.kych.net。
    # （后端 CORS 配置仍然保留，因为 SSR 走 127.0.0.1 不经过 CORS，
    #  浏览器同源请求也不触发 CORS preflight——这层 nginx 只是顺路。）
    location /api/    { proxy_pass http://127.0.0.1:8000; proxy_set_header Host $host; proxy_set_header X-Real-IP $remote_addr; proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for; proxy_set_header X-Forwarded-Proto $scheme; }
    location /uploads/{ proxy_pass http://127.0.0.1:8000; proxy_set_header Host $host; expires 30d; add_header Cache-Control "public, immutable"; }
    location /avatars/{ proxy_pass http://127.0.0.1:8000; proxy_set_header Host $host; expires 7d;  add_header Cache-Control "public"; }
}
```

```bash
sudo ln -s /etc/nginx/sites-available/gokych-frontend /etc/nginx/sites-enabled/
sudo certbot --nginx -d eo.kych.net --agree-tos -m you@kych.net
sudo nginx -t && sudo systemctl reload nginx
```

> 这样 `eo.kych.net` 与 `api.kych.net` 都是独立 server 块，
> 浏览器看到的是**同 origin**（`/api/*` 在 `eo.kych.net` 上），
> 完全不触发 CORS，cookie 天然带上。后端 `.env` 里 `CORS_ALLOWED_ORIGINS`
> 此时可填 `https://eo.kych.net`（同源场景其实用不到，但留着
> 兼容 dev/proxy 备用路径，0 成本）。

### 3.6 腾讯云 EdgeOne 接入

EdgeOne 控制台路径与字段名以腾讯云实际界面为准（平台会迭代），下面
只列客观不变的配置项：

1. **添加站点**：把 `kych.net`（你拥有的主域）加入 EdgeOne，
   套餐选"安全加速"或"内容加速"均可（本场景无大文件下载，基础即可）。
2. **接入子域名加速**：
   - `eo.kych.net`：加速类型选"网页/动态加速"或"通用加速"，
     **源站配置**填 `<VM 公网 IP>:443`，协议选 HTTPS 回源，回源
     Host 填 `eo.kych.net`。回源 SNI 同上。
   - `api.kych.net`：同上，源站 `<VM 公网 IP>:443`，回源协议
     HTTPS，回源 Host `api.kych.net`。
3. **HTTPS 证书**：EdgeOne 控制台"域名管理 → HTTPS 证书"上传 /
   申请新证书（EdgeOne 支持免费 DV 证书一键签发），绑定到两个子域。
4. **DNS 切换**：EdgeOne 会给出一个 `xxx.eo.dnsecdn.com` 形式的
   CNAME，把它加到你 DNS 里 `eo.kych.net` / `api.kych.net`
   的 CNAME 记录。如果主域本身也托管在 EdgeOne（Edge DNS 模式），
   直接在 EdgeOne 里加 NS 记录即可。
5. **缓存策略**（可选但推荐）：
   - `eo.kych.net/.next/static/*` → 缓存 30 天 immutable
     （Next 用 hashed filename，长期缓存安全）
   - `eo.kych.net/_next/static/*`（同上，老命名）
   - `eo.kych.net/api/*` → 遵循源站 Cache-Control，
     不强制缓存（动态接口不缓存）
   - `api.kych.net/uploads/*`、`/avatars/*` → 缓存 7 天
     （uploads 内容哈希命名，缓存安全）
6. **回源健康检查**：开启对 `api.kych.net/api/health` 的探探测，
   异常时 EdgeOne 告警。

> EdgeOne 没有内置的 Git 自动构建 / PR preview 第一类集成
> 集成；前端发布走 §3.3 的 `tar → scp → systemctl restart` 流程，
> 或挂 CI（GitHub Actions / Gitee Go）跑同一脚本即可。要做"预览
> 环境"就再开一台小 VM 跑同样一套，CNAME 一个 `gokych-stg.kych.net`
> 到它的 EdgeOne 加速域名。

### 3.7 自动发布（可选，CI 接管）

仓库可以加一条 `.github/workflows/deploy-frontend.yml`（或 Gitee Go
的等价流水线），`on: push to main` 时跑：

```yaml
- run: cd web && npm ci && npm run build
- run: tar -czf next-server.tgz -C .next/standalone . -C .. .next/static -C .. public
- uses: appleboy/scp-action@v0.1.7
  with: { host: api.kych.net, username: deploy, key: ${{secrets.SSH_KEY}}, source: next-server.tgz, target: /tmp/ }
- uses: appleboy/ssh-action@v1.0.3
  with:
    host: api.kych.net
    username: deploy
    key: ${{secrets.SSH_KEY}}
    script: |
      sudo rm -rf /opt/next-server/* && sudo tar -xzf /tmp/next-server.tgz -C /opt/next-server
      sudo cp -r /opt/next-server/.next/static /opt/next-server/.next/static
      sudo chown -R nextjs:nextjs /opt/next-server
      sudo systemctl restart next-server
```

---

## 4. DNS

| 记录 | 名称              | 类型  | 值                              |
|------|-------------------|-------|---------------------------------|
| A    | `kych.net`        | A     | `<VM 公网 IP>`                  |
| AAAA | `kych.net`        | AAAA  | `<VM IPv6>`（如有）             |
| A    | `api.kych.net`    | A     | `<VM 公网 IP>`                  |
| AAAA | `api.kych.net`    | AAAA  | `<VM IPv6>`（如有）            |
| CNAME| `eo.kych.net`     | CNAME | `<EdgeOne 分配的加速域名>`      |

> `kych.net` 和 `api.kych.net` 直连 VM（A 记录），certbot 在 VM
> 上签它们的 Let's Encrypt 证书。`eo.kych.net` 走 EdgeOne
> （CNAME），EdgeOne 自动签发/管理边缘 HTTPS 证书；EdgeOne 边缘
> 节点回源到 VM 的 :443，源站证书由 certbot 签（上面 §2.6 一起搞了）。

---

## 5. 端到端验证清单

部署完后按这个顺序过一遍：

1. `curl -fsS https://api.kych.net/healthz` → `{"db":"ok","status":"ok"}`
2. `curl -fsS https://eo.kych.net/` → 200 渲染首页（不要 curl /api/*，那是后端）
3. 浏览器开 `https://eo.kych.net/admin`，登录 admin
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
生产 `APP_DOMAIN=https://eo.kych.net` 会弹出来是
`eo.kych.net`，完全正确。**RPID 必须是 eTLD+1**（不能跨
主域），所以前端域名和后端域名必须是同一棵域树的子域（都挂在
`kych.net` 下）。

---

## 6. 监控（够用就行）

最小集：

- **uptime**：EdgeOne 控制台自带"实时监控"（请求量 / 状态码 / 回源健康），
  配 `https://api.kych.net/api/health` 与 `https://eo.kych.net/`
  两个 URL 探测；也可叠加云监控 / UptimeRobot 等外部探针
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
| Passkey 需要 HTTPS | WebAuthn 协议硬性要求 secure context | EdgeOne 自动 HTTPS（边缘节点）；VM 源站走 certbot 或 EdgeOne 回源证书 |
| RPID 域要匹配 | RPID 必须是 eTLD+1，且必须等于 `APP_DOMAIN` 解析出的 host | 前/后端都用 `*.kych.net` 子域 |
| Session cookie `Secure` | release 模式自动开 Secure，开发用 HTTP 测 cookie 不会带 | `.env` 一定设 `GIN_MODE=release` |
| MySQL `caching_sha2_password` | Go driver 默认支持，但需要 MySQL 8.0+ | 已在 docker-compose 锁定 8.0 |
| 时间戳时区 | `created_at` 走 MySQL server time；用 UTC | `data/` 里 `settings.yml` 改 timezone 或忽略（展示走浏览器 locale） |
| Uploads 体积 | nginx 默认 `client_max_body_size 1m` | 上面 nginx 配置改成 `50m` |
| 数据目录权限 | systemd `ProtectSystem=strict` 不给 /opt/gokych/data 写权限就起不来 | 配 `ReadWritePaths=/opt/gokych/data`（已加） |
| 跨域 cookie | CORS + `credentials: "include"` 不允许 `*` 源 | 后端用 `CORS_ALLOWED_ORIGINS` 显式列 |
| 上传 URL 相对路径 | 跨域下浏览器 404 | 已修：后端用 `PUBLIC_URL` 拼绝对路径（commit `90498db`），前端 `f.url` 直接用 |
| Passkey 突然 503 | `APP_DOMAIN` 改错 / RPID 跟前端 host 不匹配 | `journalctl -u gokych | grep passkey` 看 startup log；RPID 必须是 eTLD+1 |
| Node 版本不一致 | standalone `server.js` 要求运行时 Node ≥ 20（与构建端一致） | VM 上装 `nodejs-20`；systemd unit 显式 `/usr/bin/node` 校验路径 |
| CORS preflight 走错中间件 | OPTIONS 请求如果被 CSRF 中间件拦了，浏览器永远收不到 204 → 实际 mutation 也发不出去 | CORS 已在 `csrfMiddleware` 之前安装，preflight 直接 204 短路（commit `90498db`） |

---

## 8. 回滚预案

| 场景                    | 怎么办                                              |
|-------------------------|------------------------------------------------------|
| 后端起不来              | `sudo systemctl restart gokych`；不行就 `cp gokych.prev gokych` 然后 restart |
| Passkey 突然挂          | 看 `journalctl -u gokych | grep passkey`；`APP_DOMAIN` 改错就 503 |
| 前端构建失败            | tar 包没推上去前先在本地 `npm run build` 跑一遍；上线失败用 §3.3 脚本回滚到 `/opt/next-server.prev/` 并 restart |
| 数据库升级挂了          | `mv /var/backups/gokych/db-latest.sql.gz /tmp/`，从备份恢复（drop + create + 灌入） |
| 整个翻车                | 重新跑 §2 整套；10 分钟内能恢复（VM 不挂 + DNS 不挂就 OK） |

---

## 9. 一行话总结

> **Ubuntu 24 VM 上跑 gokych 单实例（systemd + nginx + MySQL 8）+ 同机
> `next-server`（standalone `server.js`），腾讯云 EdgeOne 在边缘接
> CDN/HTTPS 并回源到 VM。同 origin 部署（nginx 把 `/api/*` 顺路转给
> :8000）让浏览器几乎不触发 CORS；CORS + `PUBLIC_URL` 仍是 dev /
> 跨域备用路径的兜底。`.env` 里 `CORS_ALLOWED_ORIGINS` /
> `PUBLIC_URL` / `APP_DOMAIN` 这三个是生产正确性的关键 — 改完
> `.env` 必须 `systemctl restart gokych`，改完前端必须
> `systemctl restart next-server`。**

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
systemd / nginx / MySQL / TLS（首次部署 + `--update` 后续更新都支持）：

```bash
# 首次部署：自动建用户、装包、初始化 MySQL、写 systemd、写 nginx(HTTP-only)、
#           certbot --nginx 签 TLS（3 个域名：api.kych.net / eo.kych.net / kych.net）
./scripts/deploy-backend.sh

# 后续更新：只重传二进制 + 重启（.env 里的密钥自动从远端读回保留）
./scripts/deploy-backend.sh --update

# ARM64 Ubuntu VM（AWS Graviton / 树莓派）
GOARCH=arm64 ./scripts/deploy-backend.sh
```

**`scripts/deploy-frontend.sh`** — 本地构建 Next.js standalone，推到
VM 的 `/opt/next-server/`，装 Node.js 20 + 写 next-server systemd
（首次部署 + `--update` 后续更新都支持）：

```bash
# 首次：装 Node 20 + 建 nextjs 用户 + 写 next-server.service + 推产物
./scripts/deploy-frontend.sh

# 后续更新：只构建 + 推 + 重启
./scripts/deploy-frontend.sh --update
```

四个脚本的职责切分：
- `build-release.sh`    → 多平台二进制 + 哈希
- `install-backend.sh`  → 单机装（适合 macOS 本地、临时测试、容器）
- `deploy-backend.sh`   → 远程 VM 后端整套（适合生产）
- `deploy-frontend.sh`  → 远程 VM 前端 Next.js standalone（适合生产）

---

## 附录 A：相关 commit 一览（按时间倒序）

| commit    | 主题                                                         |
|-----------|--------------------------------------------------------------|
| (pending) | chore: add scripts/deploy-backend.sh (一键部署)              |
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

把前端从"计划上 CF Pages"切到"EdgeOne 回源 + VM 跑 Node standalone"
对代码的改动极小，**只动一个文件**：

- `web/next.config.ts` 新增 `output: "standalone"`。
  - 之前仓库里一直没设这一项；`web/Dockerfile` 第 35 行的
    `COPY --from=builder /app/.next/standalone ./` 已经在按"standalone
    已开启"的假设组装运行时镜像——属于一个长期存在的隐式不一致。
    本方案顺手修上，CI 的 `npm run build` / EdgeOne 源站构建都走同一条
    standalone 输出，行为一致。
  - **不要**配套加 `@cloudflare/next-on-pages`、`experimental.runtime:"edge"`、
    `build:cf` 脚本——那些是 CF Pages 的 V8-isolate 路线专属，本方案
    走 Node server，加了反而会触发 Edge runtime 约束把 `dompurify`
    等依赖干掉。

- 其它（§3 的 systemd unit、nginx 站点、EdgeOne 控制台配置）都是
  VM / 云控制台层面的操作，不进仓库；把 §3.3 那段 `tar → scp →
  systemctl restart` 包成 `scripts/deploy-frontend.sh` 的话再单独
  提一个 commit，与现有 `scripts/deploy-backend.sh` 并列。

