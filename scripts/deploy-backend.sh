#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# gokych 后端一键部署脚本 — Ubuntu 24.04 VM
#
# 域名架构:
#   kych.net        → 301 跳转到 eo.kych.net（主域名，直连 VM）
#   api.kych.net    → VM → :8000（后端 API + 上传/头像静态资源）
#   eo.kych.net     → EdgeOne → VM → nginx → :3000(前端) + :8000(/api/*)
#
# SSL 证书鸡生蛋问题:
#   nginx 启动 443 需要 SSL 证书；certbot 签证书需要域名通过 HTTP-01
#   验证，而 HTTP-01 验证需要 nginx 的 80 端口能响应。
#
#   解法: 先只写 listen 80（纯 HTTP，不引用任何证书文件）→ nginx 能
#   正常启动 → certbot --nginx 自动用 80 端口跑 HTTP-01 验证、签证书、
#   然后自动往 nginx 配置里插入 listen 443 ssl 块 + 80→443 跳转 →
#   reload nginx。全程零停机、零手动编辑证书路径。
#
# 用法（首次部署）:
#   ./scripts/deploy-backend.sh
#
# 用法（更新二进制）:
#   ./scripts/deploy-backend.sh --update
#
# 可调 env var（所有都有默认值，缺啥问啥）:
#   REMOTE_HOST    远端 VM 的 IP / 域名（必填，交互式）
#   REMOTE_USER    远端登录用户（默认 root）
#   REMOTE_PORT    SSH 端口（默认 22）
#   MAIN_DOMAIN    主域名（默认 kych.net）
#   API_DOMAIN     API 域名（默认 api.kych.net）
#   EO_DOMAIN      前端域名（默认 eo.kych.net）
#   EMAIL          Let's Encrypt 注册邮箱（必填，交互式）
#   DB_PASSWORD    MySQL 密码（不传则生成随机 48 字符）
#   SESSION_SECRET 会话密钥（不传则生成随机 48 字符）
#   GOARCH         linux/amd64 或 linux/arm64（默认 amd64）
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

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
    --help|-h)   sed -n '2,30p' "$0"; exit 0 ;;
    *) die "unknown arg: $1 (try --help)" ;;
  esac
done

# ── 1. 收集配置 ──
log "收集部署配置…"
[[ -z "${REMOTE_HOST:-}" ]] && { read -rp "  远端 VM IP / 域名: " REMOTE_HOST; [[ -n "$REMOTE_HOST" ]] || die "REMOTE_HOST 必填"; }
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_PORT="${REMOTE_PORT:-22}"
MAIN_DOMAIN="${MAIN_DOMAIN:-kych.net}"
API_DOMAIN="${API_DOMAIN:-api.kych.net}"
EO_DOMAIN="${EO_DOMAIN:-eo.kych.net}"
[[ -z "${EMAIL:-}" ]] && { read -rp "  Let's Encrypt 邮箱: " EMAIL; [[ -n "$EMAIL" ]] || die "EMAIL 必填"; }
GOARCH="${GOARCH:-amd64}"
GOOS="${GOOS:-linux}"

gen_secret() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 48 || true; }
[[ -z "${DB_PASSWORD:-}" ]]    && DB_PASSWORD="$(gen_secret)"
[[ -z "${SESSION_SECRET:-}" ]] && SESSION_SECRET="$(gen_secret)"

# 更新模式：从远端 .env 读回 DB_PASSWORD / SESSION_SECRET
REMOTE_ENV=/opt/gokych/.env
if [[ "$UPDATE_ONLY" -eq 1 ]]; then
  EXISTING_ENV=$(ssh -p "$REMOTE_PORT" -o BatchMode=yes -o ConnectTimeout=5 \
                 "${REMOTE_USER}@$REMOTE_HOST" "cat '$REMOTE_ENV' 2>/dev/null || true" 2>/dev/null || true)
  if [[ -n "$EXISTING_ENV" ]]; then
    DB_PASSWORD=$(grep -E '^DB_PASSWORD=' <<<"$EXISTING_ENV" | head -1 | cut -d= -f2-)
    SESSION_SECRET=$(grep -E '^SESSION_SECRET=' <<<"$EXISTING_ENV" | head -1 | cut -d= -f2-)
    : "${DB_PASSWORD:=$(gen_secret)}"
    : "${SESSION_SECRET:=$(gen_secret)}"
    warn "更新模式：从远端 .env 读回密钥"
  fi
fi

# 派生配置
APP_DOMAIN="https://${EO_DOMAIN}"          # Passkey RPID = EO_DOMAIN 的 host
PUBLIC_URL="https://${API_DOMAIN}"          # /uploads/* 绝对 URL 前缀
CORS_ORIGIN="https://${EO_DOMAIN}"          # 浏览器跨域（EdgeOne 同源时用不到，但 dev/备用路径要）

cat <<EOF
  ─────────────────────────────────────────
  REMOTE_HOST:     $REMOTE_HOST
  MAIN_DOMAIN:     $MAIN_DOMAIN  (→ 301 跳转到 $EO_DOMAIN)
  API_DOMAIN:      $API_DOMAIN   (→ VM :8000)
  EO_DOMAIN:      $EO_DOMAIN    (→ EdgeOne → VM :3000)
  APP_DOMAIN:      $APP_DOMAIN   (Passkey)
  PUBLIC_URL:      $PUBLIC_URL   (uploads 绝对 URL)
  CORS_ORIGIN:     $CORS_ORIGIN
  EMAIL:           $EMAIL
  GOOS/GOARCH:     $GOOS/$GOARCH
  DB_PASSWORD:     ${DB_PASSWORD:0:6}…(48 字符)
  SESSION_SECRET:  ${SESSION_SECRET:0:6}…(48 字符)
  ─────────────────────────────────────────
EOF
echo
if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  read -rp "  以上配置对吗？(y/N) " ans
  [[ "$ans" =~ ^[Yy]$ ]] || die "取消"
fi
echo

# ── 2. SSH 预检 ──
log "检查 SSH 连通性…"
ssh -p "$REMOTE_PORT" -o BatchMode=yes -o ConnectTimeout=5 \
    -o StrictHostKeyChecking=accept-new \
    "${REMOTE_USER}@$REMOTE_HOST" true 2>/dev/null || \
  die "无法 ssh 到 ${REMOTE_USER}@$REMOTE_HOST:$REMOTE_PORT"
ok "SSH OK"

# ── 3. 本地交叉编译 ──
log "本地交叉编译 (linux/${GOARCH})…"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_PATH="/tmp/gokych.linux.$GOARCH"
(cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
  go build -ldflags='-s -w' -trimpath -o "$BIN_PATH" ./cmd/gokych)
ok "二进制: $BIN_PATH ($(du -h "$BIN_PATH" | cut -f1))"

rsh()  { ssh -p "$REMOTE_PORT" "${REMOTE_USER}@$REMOTE_HOST" "$@"; }
rscp() { scp -P "$REMOTE_PORT" "$1" "${REMOTE_USER}@$REMOTE_HOST:$2"; }

# ═══════════════════════════════════════════════════════════════════════
# 首次部署
# ═══════════════════════════════════════════════════════════════════════
if [[ "$UPDATE_ONLY" -eq 0 ]]; then

  # ── 4a. 系统包 ──
  log "远端：安装系统包…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq mysql-server nginx certbot python3-certbot-nginx rsync
REMOTE
  ok "系统包就绪"

  # ── 4b. 防火墙 ──
  log "远端：防火墙（22/80/443）…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
command -v ufw >/dev/null 2>&1 || apt-get install -y -qq ufw
ufw --force reset >/dev/null 2>&1
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable >/dev/null 2>&1
REMOTE
  ok "防火墙就绪"

  # ── 4c. deploy 用户 ──
  log "远端：创建 deploy 用户…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
id deploy >/dev/null 2>&1 || adduser --disabled-password --gecos "" deploy
mkdir -p /home/deploy/.ssh
cp /root/.ssh/authorized_keys /home/deploy/.ssh/ 2>/dev/null || true
chown -R deploy:deploy /home/deploy/.ssh
chmod 700 /home/deploy/.ssh
echo "deploy ALL=(ALL) NOPASSWD: /bin/systemctl restart gokych, /bin/systemctl restart next-server, /bin/systemctl status gokych, /bin/systemctl status next-server" \
  >/etc/sudoers.d/deploy-gokych
REMOTE
  ok "用户就绪"

  # ── 4d. MySQL ──
  log "远端：初始化 MySQL…"
  rsh bash -s <<REMOTE
set -euo pipefail
DB_PASS="${DB_PASSWORD}"
mysql -e "CREATE DATABASE IF NOT EXISTS gokych CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
mysql -e "CREATE USER IF NOT EXISTS 'gokych'@'127.0.0.1' IDENTIFIED BY '\${DB_PASS}';"
mysql -e "ALTER USER 'gokych'@'127.0.0.1' IDENTIFIED BY '\${DB_PASS}';"
mysql -e "GRANT ALL PRIVILEGES ON gokych.* TO 'gokych'@'127.0.0.1';"
mysql -e "FLUSH PRIVILEGES;"
REMOTE
  ok "MySQL 就绪（db=gokych）"

  # ── 4e. 目录 ──
  log "远端：建部署目录…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
mkdir -p /opt/gokych/bin /opt/gokych/data/{uploads,avatars,settings,plugins,themes,typst}
mkdir -p /opt/next-server
chown -R deploy:deploy /opt/gokych /opt/next-server
REMOTE
  ok "目录就绪"

  # ── 4f. nginx 配置（HTTP-only — 不引用任何 SSL 证书文件）──────────
  #   这是解决"nginx 启动 443 需要证书 / certbot 签证书需要 nginx 在
  #   80 端口响应 HTTP-01 验证"矛盾的关键：先只写 listen 80，不写任何
  #   ssl_certificate 指令，nginx 能正常启动；然后 certbot --nginx
  #   自动用 80 端口跑 HTTP-01 验证、签证书、自动插入 443 ssl 块。
  log "远端：写 nginx 配置（HTTP-only）…"
  rsh bash -s <<REMOTE
set -euo pipefail
MAIN='${MAIN_DOMAIN}'
API='${API_DOMAIN}'
EO='${EO_DOMAIN}'

cat >/etc/nginx/sites-available/gokych <<NGINX
# ─ ${API} ─ 后端 API + 静态资源 ─
server {
    listen 80;
    server_name ${API};

    client_max_body_size 50m;

    location /api/ {
        proxy_pass http://127.0.0.1:8000;
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

# ─ ${EO} ─ 前端(SSR) + API 顺路转发(同 origin, 不触发 CORS) ─
server {
    listen 80;
    server_name ${EO};

    client_max_body_size 50m;

    location /api/ {
        proxy_pass http://127.0.0.1:8000;
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
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade           \$http_upgrade;
        proxy_set_header Connection        "upgrade";
        proxy_read_timeout 60s;
    }
}

# ─ ${MAIN} ─ 主域名 301 跳转到 EdgeOne 加速的前端 ─
server {
    listen 80;
    server_name ${MAIN};
    return 301 https://${EO}\$request_uri;
}
NGINX

rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
nginx -t
systemctl restart nginx
REMOTE
  ok "nginx HTTP 配置就绪（端口 80，无 SSL）"

  # ── 4g. 签 TLS 证书 ──
  #   certbot --nginx 会:
  #     1. 用 80 端口跑 HTTP-01 验证（nginx 已在 80 上响应）
  #     2. 签发证书
  #     3. 自动往 nginx 配置里插入 listen 443 ssl + 证书路径
  #     4. 自动给 listen 80 块加 return 301 → https
  #     5. reload nginx
  log "远端：certbot 签证书（3 个域名）…"
  rsh bash -s <<REMOTE
set -euo pipefail
certbot --nginx \
  -d ${API_DOMAIN} -d ${EO_DOMAIN} -d ${MAIN_DOMAIN} \
  --non-interactive --agree-tos -m ${EMAIL} --redirect
REMOTE
  ok "TLS 证书已签发 + HTTPS 自动配置完成"

elif [[ "$UPDATE_ONLY" -eq 1 ]]; then
  # ── 更新模式只需 reload nginx（配置没变）──
  rsh "nginx -t 2>&1 && systemctl reload nginx 2>&1" || true
fi

# ═══════════════════════════════════════════════════════════════════════
# 首次 + 更新都做
# ═══════════════════════════════════════════════════════════════════════

# ── 5. systemd unit ──
if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  log "远端：写 gokych.service…"
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
REMOTE
  ok "systemd unit 就位"
fi

# ── 6. .env ──
log "写 .env…"
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
APP_DOMAIN=https://${EO_DOMAIN}
PUBLIC_URL=https://${API_DOMAIN}
CORS_ALLOWED_ORIGINS=https://${EO_DOMAIN}
TRUSTED_PROXIES=127.0.0.1
ENV
chmod 600 "$TMP_ENV"
rscp "$TMP_ENV" "$REMOTE_ENV"
rsh "chown deploy:deploy '$REMOTE_ENV' && chmod 600 '$REMOTE_ENV'"
rm -f "$TMP_ENV"
ok ".env 已上传"

# ── 7. 上传二进制（带回滚保险）──
log "上传二进制…"
rsh "if [ -f /opt/gokych/bin/gokych ]; then cp /opt/gokych/bin/gokych /opt/gokych/bin/gokych.prev; fi"
rscp "$BIN_PATH" /opt/gokych/bin/gokych
rsh "chown deploy:deploy /opt/gokych/bin/gokych && chmod +x /opt/gokych/bin/gokych"
ok "二进制就位（回滚: cp gokych.prev gokych）"

# ── 8. 启动 / 重启 ──
log "启动 gokych…"
rsh "systemctl restart gokych"
sleep 2
rsh "systemctl --no-pager --full status gokych | head -15 || true"

# ── 9. 健康检查 ──
log "健康检查…"
sleep 1
if rsh "curl -fsS http://127.0.0.1:8000/api/health" >/dev/null 2>&1; then
  ok "127.0.0.1:8000 OK"
else
  warn "本地 health 失败 — journalctl -u gokych"
  rsh "journalctl -u gokych --no-pager -n 20" || true
fi
echo

if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  log "外网 HTTPS 检查…"
  curl -fsS "https://${API_DOMAIN}/healthz" 2>/dev/null && ok "https://${API_DOMAIN}/healthz OK" || \
    warn "外网 health 失败 — DNS 可能还没指过来，稍等再试"
fi
echo

ok "后端部署完成 🚀"
echo
cat <<NEXT
下一步：
  1. 浏览器打开 https://${EO_DOMAIN}/admin，用 admin / admin123 登录
     （需先跑 deploy-frontend.sh 部署前端）
  2. ./scripts/deploy-frontend.sh   ← 部署 Next.js 前端
  3. EdgeOne 控制台：把 ${EO_DOMAIN} CNAME 到 EdgeOne 加速域名，
     回源地址填 ${REMOTE_HOST}，回源 Host 填 ${EO_DOMAIN}，协议 HTTPS
  4. 备份：crontab -e 加一行  30 3 * * * /opt/gokych/bin/backup.sh
  5. 关键凭据在 /opt/gokych/.env (chmod 600):
     SESSION_SECRET, DB_PASSWORD
NEXT