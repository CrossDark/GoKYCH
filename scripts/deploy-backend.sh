#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# gokych backend 一键部署脚本
#
# 从 macOS / Linux 开发机把后端二进制 + 配置推到一台 Ubuntu 24.04
# VM 上，过程中处理：建用户、装包、初始化 MySQL、写 systemd、配置
# nginx、签 TLS 证书。脚本是幂等的 — 既能首次部署，也能后续更新
# （默认走"更新"路径，跳过已经做过的步骤）。
#
# 用法（首次部署）：
#   ./scripts/deploy-backend.sh
#
# 用法（更新二进制）：
#   ./scripts/deploy-backend.sh --update
#
# 用法（指定远端 / 域名）：
#   REMOTE_HOST=1.2.3.4 DOMAIN=api.example.com \
#   CORS_ORIGIN=https://gokych.example.com \
#   ./scripts/deploy-backend.sh
#
# 可调的 env var（所有都有默认值，缺啥问啥）：
#   REMOTE_HOST         远端 VM 的 IP / 域名（必填）
#   REMOTE_USER         远端登录用户（默认 deploy，会自动创建）
#   REMOTE_PORT         SSH 端口（默认 22）
#   DOMAIN              API 的对外域名（默认 api.example.com）
#   EMAIL               Let's Encrypt 注册邮箱（必填）
#   CORS_ORIGIN         允许跨域的前端 origin（默认 https://<DOMAIN>）
#   APP_DOMAIN          WebAuthn RPID（默认 CORS_ORIGIN 的 host）
#   PUBLIC_URL          /uploads/* 绝对 URL 前缀（默认 https://<DOMAIN>）
#   DB_PASSWORD         MySQL 密码（不传则生成随机 32 字节）
#   SESSION_SECRET      会话密钥（不传则生成随机 32 字节）
#   GOARCH              linux/amd64 或 linux/arm64（默认 amd64）
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── 颜色 + 输出 ──
RED='\033[0;31m'; GRN='\033[0;32m'; YEL='\033[0;33m'; BLU='\033[0;34m'; NC='\033[0m'
log()  { printf "${BLU}==>${NC} %s\n" "$*"; }
ok()   { printf "${GRN}✓${NC} %s\n" "$*"; }
warn() { printf "${YEL}!${NC} %s\n" "$*" >&2; }
die()  { printf "${RED}✗${NC} %s\n" "$*" >&2; exit 1; }

# ── 解析参数 ──
UPDATE_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --update|-u) UPDATE_ONLY=1; shift ;;
    --help|-h)
      sed -n '2,30p' "$0"; exit 0 ;;
    *) die "unknown arg: $1 (try --help)" ;;
  esac
done

# ── 1. 收集配置 ──
log "收集部署配置…"
[[ -z "${REMOTE_HOST:-}" ]]   && { read -rp "  远端 VM IP / 域名: " REMOTE_HOST; [[ -n "$REMOTE_HOST" ]] || die "REMOTE_HOST 必填"; }
REMOTE_USER="${REMOTE_USER:-deploy}"
REMOTE_PORT="${REMOTE_PORT:-22}"
DOMAIN="${DOMAIN:-api.example.com}"
[[ -z "${EMAIL:-}" ]]         && { read -rp "  Let's Encrypt 邮箱: " EMAIL; [[ -n "$EMAIL" ]] || die "EMAIL 必填"; }
CORS_ORIGIN="${CORS_ORIGIN:-https://$DOMAIN}"
APP_DOMAIN="${APP_DOMAIN:-${CORS_ORIGIN#https://}}"
APP_DOMAIN="${APP_DOMAIN#http://}"
PUBLIC_URL="${PUBLIC_URL:-https://$DOMAIN}"
GOARCH="${GOARCH:-amd64}"
GOOS="${GOOS:-linux}"

# 密码类：没传就生成。生成时用 /dev/urandom，比 openssl rand 跨平台一致
#（macOS / Linux 都跑同一段不会因为 openssl 路径/版本不同出岔子）。
#
# 注意：`head -c N` 一旦读够 N 字节就关管道，tr 收到 SIGPIPE 退出，
# 在 `set -o pipefail` 下整条管道会被标为失败。`|| true` 兜一下。
gen_secret() {
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 48 || true
}
[[ -z "${DB_PASSWORD:-}" ]]    && DB_PASSWORD="$(gen_secret)"
[[ -z "${SESSION_SECRET:-}" ]] && SESSION_SECRET="$(gen_secret)"

# 更新路径下，从远端现有 .env 读回 DB_PASSWORD / SESSION_SECRET — 重新
# 生成会把 MySQL 锁外面（DB 用户密码对不上）。如果远端 .env 不存在
#（意外情况），按全新安装走 — 此时上面生成的随机值会被用上。
if [[ "$UPDATE_ONLY" -eq 1 ]]; then
  EXISTING_ENV=$(ssh -p "$REMOTE_PORT" -o BatchMode=yes -o ConnectTimeout=5 \
                 "root@$REMOTE_HOST" "test -f '$REMOTE_ENV' && cat '$REMOTE_ENV' || true" 2>/dev/null || true)
  if [[ -n "$EXISTING_ENV" ]]; then
    [[ -z "${DB_PASSWORD_OVERRIDE:-}" ]]    && DB_PASSWORD=$(grep -E '^DB_PASSWORD='    <<<"$EXISTING_ENV" | head -1 | cut -d= -f2-)
    [[ -z "${SESSION_SECRET_OVERRIDE:-}" ]] && SESSION_SECRET=$(grep -E '^SESSION_SECRET=' <<<"$EXISTING_ENV" | head -1 | cut -d= -f2-)
    : "${DB_PASSWORD:=$(gen_secret)}"
    : "${SESSION_SECRET:=$(gen_secret)}"
    warn "更新模式：从远端 .env 读回 DB_PASSWORD / SESSION_SECRET（保活连接）"
  fi
  # 旧 .env 没有这两个 key 时，上面：= 会重新生成；但那是空字符串情况。
  # 兜底再生成一次（这次没用 pipefail 影响）：
  if [[ -z "${DB_PASSWORD:-}" ]];    then DB_PASSWORD="$(gen_secret)";    fi
  if [[ -z "${SESSION_SECRET:-}" ]]; then SESSION_SECRET="$(gen_secret)"; fi
fi

cat <<EOF
  REMOTE_HOST:        $REMOTE_HOST
  REMOTE_USER:        $REMOTE_USER
  REMOTE_PORT:        $REMOTE_PORT
  DOMAIN:             $DOMAIN
  CORS_ORIGIN:        $CORS_ORIGIN
  APP_DOMAIN:         $APP_DOMAIN
  PUBLIC_URL:         $PUBLIC_URL
  EMAIL:              $EMAIL
  GOOS/GOARCH:        $GOOS/$GOARCH
  DB_PASSWORD:        ${DB_PASSWORD:0:6}…(已生成 48 字符)
  SESSION_SECRET:     ${SESSION_SECRET:0:6}…(已生成 48 字符)
EOF
echo
if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  read -rp "  以上配置对吗？(y/N) " ans
  [[ "$ans" =~ ^[Yy]$ ]] || die "取消，编辑脚本头或重设 env 后重跑"
fi
echo

# ── 2. SSH 预检查 ──
log "检查 SSH 连通性…"
if ! ssh -p "$REMOTE_PORT" -o BatchMode=yes -o ConnectTimeout=5 \
     -o StrictHostKeyChecking=accept-new \
     "root@$REMOTE_HOST" true 2>/dev/null; then
  die "无法 ssh 到 root@$REMOTE_HOST:$REMOTE_PORT — 请先确认 VM 已部署好且 22 端口通"
fi
ok "ssh root@$REMOTE_HOST OK"

# ── 3. 本地编译 ──
log "本地交叉编译二进制 (linux/${GOARCH})…"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_PATH="/tmp/gokych.linux.$GOARCH"
(cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
  go build -ldflags='-s -w' -trimpath -o "$BIN_PATH" ./cmd/gokych)
ok "二进制已生成: $BIN_PATH ($(du -h "$BIN_PATH" | cut -f1))"

# ── 4. 远端初始化（仅首次部署时执行） ──
REMOTE_ENV=/opt/gokych/.env
REMOTE_DATA=/opt/gokych/data
REMOTE_BIN=/opt/gokych/bin/gokych
REMOTE_UNIT=/etc/systemd/system/gokych.service
REMOTE_NGINX=/etc/nginx/sites-available/gokych

# 用一个 shell 函数把整段远端命令封起来 — 这样下面既能让单条 ssh
# 调用做完整动作（无状态丢失），又不会把整段塞进一个超长 here-doc
# 看着乱。每条命令 ssh 单独调一次，便于看日志。
rsh() {
  ssh -p "$REMOTE_PORT" "root@$REMOTE_HOST" "$@"
}
rsync_to() {
  rsync -az -e "ssh -p $REMOTE_PORT" "$1" "root@$REMOTE_HOST:$2"
}

if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  log "远端：安装系统包（mysql-server / nginx / certbot）…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq mysql-server nginx certbot python3-certbot-nginx rsync
ok "系统包装好"
REMOTE

  log "远端：开防火墙（22 / 80 / 443）…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
if ! command -v ufw >/dev/null 2>&1; then
  apt-get install -y -qq ufw
fi
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
ok "防火墙开启"
REMOTE

  log "远端：创建 deploy 用户…"
  rsh bash -s <<REMOTE
set -euo pipefail
if ! id deploy >/dev/null 2>&1; then
  adduser --disabled-password --gecos "" deploy
  mkdir -p /home/deploy/.ssh
  cp /root/.ssh/authorized_keys /home/deploy/.ssh/ 2>/dev/null || true
  chown -R deploy:deploy /home/deploy/.ssh
  chmod 700 /home/deploy/.ssh
fi
# deploy 用户免 sudo（systemctl 重启用）
echo "deploy ALL=(ALL) NOPASSWD: /bin/systemctl restart gokych, /bin/systemctl status gokych" >/etc/sudoers.d/deploy-gokych
ok "deploy 用户就绪"
REMOTE

  log "远端：初始化 MySQL…"
  rsh bash -s <<REMOTE
set -euo pipefail
DB_PASS="${DB_PASSWORD}"
mysql -e "CREATE DATABASE IF NOT EXISTS gokych CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
mysql -e "CREATE USER IF NOT EXISTS 'gokych'@'127.0.0.1' IDENTIFIED BY '\${DB_PASS}';"
mysql -e "ALTER USER 'gokych'@'127.0.0.1' IDENTIFIED BY '\${DB_PASS}';"
mysql -e "GRANT ALL PRIVILEGES ON gokych.* TO 'gokych'@'127.0.0.1';"
mysql -e "FLUSH PRIVILEGES;"
ok "MySQL 就绪（db=gokych / user=gokych）"
REMOTE

  log "远端：建部署目录…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
mkdir -p /opt/gokych/bin /opt/gokych/data/{uploads,avatars,settings,plugins,themes,typst}
chown -R deploy:deploy /opt/gokych
ok "/opt/gokych 就绪"
REMOTE

  log "远端：写 nginx 配置…"
  rsh bash -s <<REMOTE
set -euo pipefail
cat >/etc/nginx/sites-available/gokych <<'NGINX'
server {
    listen 80;
    server_name ${DOMAIN};
    return 301 https://\$host\$request_uri;
}
server {
    listen 443 ssl http2;
    server_name ${DOMAIN};
    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_stapling on;
    client_max_body_size 50m;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 60s;
    }
    location /uploads/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host \$host;
        expires 30d;
        add_header Cache-Control "public, immutable";
    }
    location /avatars/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host \$host;
        expires 7d;
        add_header Cache-Control "public";
    }
    location = /healthz { proxy_pass http://127.0.0.1:8000/api/health; access_log off; }
}
NGINX
  rm -f /etc/nginx/sites-enabled/default
  ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
  nginx -t
  systemctl reload nginx
  ok "nginx 配置好了"
REMOTE

  log "远端：签 TLS 证书（certbot）…"
  rsh bash -s <<REMOTE
set -euo pipefail
certbot --nginx -d ${DOMAIN} --non-interactive --agree-tos -m ${EMAIL} --redirect
ok "TLS 证书签好"
REMOTE

  log "远端：写 systemd unit…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
cat >/etc/systemd/system/gokych.service <<'UNIT'
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
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/gokych/data
PrivateTmp=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable gokych
  ok "systemd unit 就位"
REMOTE
fi  # end of "first-time only" block

# ── 5. 写 .env（首次 + 更新都做） ──
log "写 /opt/gokych/.env…"
TMP_ENV=$(mktemp)
cat >"$TMP_ENV" <<ENV
# Generated by deploy-backend.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=gokych
DB_PASSWORD=${DB_PASSWORD}
DB_NAME=gokych
DB_CHARSET=utf8mb4
DB_POOL_MIN=2
DB_POOL_MAX=10
APP_PORT=8000
GIN_MODE=release
SESSION_SECRET=${SESSION_SECRET}
ADMIN_USERNAME=admin
ADMIN_PASSWORD=admin123
DATA_DIR=/opt/gokych/data
APP_DOMAIN=${APP_DOMAIN}
PUBLIC_URL=${PUBLIC_URL}
CORS_ALLOWED_ORIGINS=${CORS_ORIGIN}
TRUSTED_PROXIES=127.0.0.1
ENV
chmod 600 "$TMP_ENV"
rsync_to "$TMP_ENV" "$REMOTE_ENV"
rsh "chown deploy:deploy '$REMOTE_ENV' && chmod 600 '$REMOTE_ENV'"
rm -f "$TMP_ENV"
ok ".env 已上传"

# ── 6. 上传二进制（带回滚保险） ──
log "上传二进制…"
rsh "if [ -f '$REMOTE_BIN' ]; then cp '$REMOTE_BIN' '$REMOTE_BIN.prev'; fi"
rsync_to "$BIN_PATH" "$REMOTE_BIN"
rsh "chown deploy:deploy '$REMOTE_BIN' && chmod +x '$REMOTE_BIN'"
ok "二进制就位 (rollback: cp ${REMOTE_BIN}.prev ${REMOTE_BIN})"

# ── 7. 重启服务 ──
log "systemctl restart gokych…"
rsh "systemctl restart gokych"
sleep 2
rsh "systemctl --no-pager --full status gokych | head -20 || true"

# ── 8. 验证 ──
log "健康检查…"
sleep 1
if rsh "curl -fsS http://127.0.0.1:8000/api/health" >/dev/null 2>&1; then
  ok "127.0.0.1:8000/api/health OK"
else
  warn "本地 health 失败 — 看下 journalctl -u gokych"
  rsh "journalctl -u gokych --no-pager -n 30" || true
fi
echo
log "外网检查…"
if curl -fsS "https://$DOMAIN/healthz" 2>/dev/null; then
  ok "https://$DOMAIN/healthz OK"
else
  warn "外网 health 失败 — 可能是 DNS 还没指过来，稍等再试"
fi
echo
ok "全部完成 🚀"
echo
cat <<NEXT
下一步：
  - 浏览器打开 https://$DOMAIN/admin，用 .env 里的 ADMIN_USERNAME /
    ADMIN_PASSWORD 登录
  - 部署前端：见 docs/deployment.md §3
  - 备份：crontab -e，加一行
      30 3 * * * /opt/gokych/bin/backup.sh
    然后用 docs/deployment.md §2.7 里的 backup.sh 脚本
  - 这次部署用到的关键凭据已存在 /opt/gokych/.env（chmod 600）。
    如果想存到 1Password / Bitwarden / 等里也行 — 关键字段：
    SESSION_SECRET, DB_PASSWORD
NEXT
