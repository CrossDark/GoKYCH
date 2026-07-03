#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# gokych 一键部署脚本 — Ubuntu 22.04 / 24.04 (在 VM 本机跑)
#
# 自动:
#   1. 检测平台 + 安装系统包 (mysql, nginx, certbot, ufw, rsync, ...)
#   2. 从 GitHub / GitCode Release 下载预编译的 gokych 二进制 + 校验 SHA256
#   3. 初始化 MySQL (gokych DB + gokych@127.0.0.1 用户 + 随机密码)
#   4. 写 /opt/gokych/.env (随机 SESSION_SECRET + 派生的 APP_DOMAIN/PUBLIC_URL/CORS)
#   5. 写 /etc/nginx/sites-available/gokych (api 反代 + 主域名 301 跳前端)
#   6. certbot --nginx 自动签证书 (HTTP-only 先 → HTTP-01 验证 → 写 443 ssl)
#   7. 写 systemd unit (gokych.service + deploy 用户)
#   8. 启动 + 健康检查 + 打印 EdgeOne 前端环境变量清单
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-all.sh \
#     | sudo bash -s -- \
#         --site-name "我的网站" \
#         --api-domain "api.example.com" \
#         --main-domain "example.com" \
#         --frontend-domain "www.example.com" \
#         --email "admin@example.com" \
#         --admin-password "ChangeMe-Strong-Pwd"
#
# 可选:
#   --release v0.2.0          装指定版本 (默认: GitHub latest)
#   --host github|gitcode     限定下载源 (默认: auto, GitHub 失败回退 gitcode)
#   --admin-username admin    管理员用户名 (默认: admin)
#   --skip-firewall           跳过 ufw 配置 (云安全组已经放行时用)
#   --skip-certbot            跳过 TLS (内网/测试环境用, 然后自己处理 443)
#   --yes                     跳过确认提示 (CI 自动化用)
#   --uninstall               卸载 (清 .env + service + nginx + db + 二进制)
#
# 卸载会保留 /var/lib/mysql 数据;清干净请加 --purge-data
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

# ── 必须 sudo (但 --help 可以免 sudo) ──
if [[ $EUID -ne 0 ]]; then
  if [[ "${1:-}" != "--help" && "${1:-}" != "-h" ]]; then
    die "请用 sudo 跑 (curl ... | sudo bash -s -- ...)"
  fi
fi

# ── 常量 ──
GH_REPO="CrossDark/GoKYCH"
GC_REPO="CrossDark/GoKych"   # GitCode 区分大小写
ASSET_PREFIX="gokych"
SUMS_FILE="SHA256SUMS"
INSTALL_DIR="/opt/gokych"
DATA_DIR="${INSTALL_DIR}/data"
BIN_PATH="${INSTALL_DIR}/bin/gokych"
ENV_FILE="${INSTALL_DIR}/.env"
SERVICE_NAME="gokych"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
NGINX_SITE="/etc/nginx/sites-available/gokych"
NGINX_LINK="/etc/nginx/sites-enabled/gokych"

# ── 默认值 ──
SITE_NAME=""
API_DOMAIN=""
MAIN_DOMAIN=""
FRONTEND_DOMAIN=""
EMAIL=""
ADMIN_USERNAME="admin"
ADMIN_PASSWORD=""
RELEASE="latest"
HOST="auto"            # auto | github | gitcode
SKIP_FIREWALL=0
SKIP_CERTBOT=0
YES=0
UNINSTALL=0
PURGE_DATA=0

# ── 帮助 ──
usage() {
  sed -n '2,40p' "$0"
  exit 0
}

# ── 参数解析 ──
while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)            usage ;;
    --site-name=*)        SITE_NAME="${1#*=}"; shift ;;
    --site-name)          SITE_NAME="${2:-}"; shift 2 ;;
    --api-domain=*)       API_DOMAIN="${1#*=}"; shift ;;
    --api-domain)         API_DOMAIN="${2:-}"; shift 2 ;;
    --main-domain=*)      MAIN_DOMAIN="${1#*=}"; shift ;;
    --main-domain)        MAIN_DOMAIN="${2:-}"; shift 2 ;;
    --frontend-domain=*)  FRONTEND_DOMAIN="${1#*=}"; shift ;;
    --frontend-domain)    FRONTEND_DOMAIN="${2:-}"; shift 2 ;;
    --email=*)            EMAIL="${1#*=}"; shift ;;
    --email)              EMAIL="${2:-}"; shift 2 ;;
    --admin-username=*)   ADMIN_USERNAME="${1#*=}"; shift ;;
    --admin-password=*)   ADMIN_PASSWORD="${1#*=}"; shift ;;
    --admin-password)     ADMIN_PASSWORD="${2:-}"; shift 2 ;;
    --release=*)          RELEASE="${1#*=}"; shift ;;
    --host=*)             HOST="${1#*=}"; shift ;;
    --skip-firewall)      SKIP_FIREWALL=1; shift ;;
    --skip-certbot)       SKIP_CERTBOT=1; shift ;;
    --yes|-y)             YES=1; shift ;;
    --uninstall)          UNINSTALL=1; shift ;;
    --purge-data)         PURGE_DATA=1; shift ;;
    *) die "未知参数: $1 (--help 看用法)" ;;
  esac
done

# ── 交互式补全 (--yes 跳过) ──
ask() {
  local var_name="$1" prompt="$2" default="${3:-}"
  local cur="${!var_name:-}"
  if [[ -n "$cur" ]]; then return 0; fi
  if [[ "$YES" -eq 1 ]]; then
    if [[ -z "$default" ]]; then die "--$var_name 必填"; fi
    printf -v "$var_name" '%s' "$default"
    return 0
  fi
  local hint=""
  [[ -n "$default" ]] && hint=" [$default]"
  read -rp "  $prompt$hint: " cur
  cur="${cur:-$default}"
  if [[ -z "$cur" ]]; then die "$var_name 必填"; fi
  printf -v "$var_name" '%s' "$cur"
}

if [[ "$UNINSTALL" -eq 0 ]]; then
  ask SITE_NAME      "网站名称 (显示在 <title> 和 header)" "我的 Wiki"
  ask API_DOMAIN     "后端域名 (api.example.com — Let’s Encrypt 会签这个)"
  ask MAIN_DOMAIN    "主域名 (example.com — 301 跳到前端域名)"
  ask FRONTEND_DOMAIN "前端域名 (www.example.com — EdgeOne 上跑 Next.js 的域名, 用于 CORS + APP_DOMAIN)"
  ask EMAIL          "Let's Encrypt 注册邮箱"
  # 强密码检查
  if [[ -z "$ADMIN_PASSWORD" ]]; then
    if [[ "$YES" -eq 1 ]]; then
      die "需要 --admin-password (CI 自动化必须显式传, 不用默认 admin123)"
    fi
    while true; do
      read -rsp "  管理员密码 (默认 admin123 也行, 输回车跳过): " ADMIN_PASSWORD
      echo
      [[ -z "$ADMIN_PASSWORD" ]] && ADMIN_PASSWORD="admin123" && break
      if [[ ${#ADMIN_PASSWORD} -lt 8 ]]; then warn "太短了 (< 8), 重输或回车用默认"; continue; fi
      break
    done
  fi
  # 派生
  APP_DOMAIN="https://${FRONTEND_DOMAIN}"
  PUBLIC_URL="https://${API_DOMAIN}"
  CORS_ORIGIN="${APP_DOMAIN}"
fi

# ── 工具函数 ──
gen_secret() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 48 || true; }

# ── 卸载分支 ──
do_uninstall() {
  log "卸载 gokych (数据保留: $DATA_DIR)"
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$SERVICE_FILE"
  systemctl daemon-reload

  rm -f "$NGINX_LINK"
  rm -f "$NGINX_SITE"
  nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null || true

  rm -f "$BIN_PATH" "${INSTALL_DIR}/bin/gokych.prev"

  mysql -e "DROP DATABASE IF EXISTS gokych;" 2>/dev/null || true
  mysql -e "DROP USER IF EXISTS 'gokych'@'127.0.0.1';" 2>/dev/null || true
  [[ "$PURGE_DATA" -eq 1 ]] && rm -rf "$DATA_DIR" && ok "数据已清除"
  ok "卸载完成 (systemd + nginx + mysql 已清理)"
  exit 0
}

[[ "$UNINSTALL" -eq 1 ]] && do_uninstall

# ── 显示配置 + 确认 ──
cat <<EOF
${BLU}─── 配置 ─────────────────────────────────────────────${NC}
  网站名称:       $SITE_NAME
  后端域名:       $API_DOMAIN    → VM :8000 (gokych)
  主域名:         $MAIN_DOMAIN   → 301 → https://${FRONTEND_DOMAIN}
  前端域名:       $FRONTEND_DOMAIN → EdgeOne 上跑 Next.js SSR
  管理员:         $ADMIN_USERNAME / $ADMIN_PASSWORD
  数据库:         gokych / gokych@127.0.0.1 (随机密码)
  SESSION_SECRET:  ${SESSION_SECRET:-<将自动生成>}
  APP_DOMAIN:      $APP_DOMAIN
  PUBLIC_URL:      $PUBLIC_URL
  CORS_ORIGIN:     $CORS_ORIGIN
${BLU}──────────────────────────────────────────────────────${NC}
EOF
echo

# 生成随机凭据
[[ -z "${SESSION_SECRET:-}" ]] && SESSION_SECRET="$(gen_secret)"
DB_PASSWORD="$(gen_secret)"

# 生成随机 admin 密码如果用户用默认
[[ "$ADMIN_PASSWORD" == "admin123" ]] || true  # 用户输入就用用户的

if [[ "$YES" -eq 0 ]]; then
  read -rp "  配置对吗? (y/N) " ans
  [[ "$ans" =~ ^[Yy]$ ]] || die "取消"
fi
echo

# ── 1. 平台检测 ──
log "检测平台…"
PLATFORM="$(detect_platform)"
GOOS="${PLATFORM%/*}"
GOARCH="${PLATFORM#*/}"
[[ "$GOOS" == "linux" ]] || die "只支持 Linux (当前: $GOOS), macOS 请用 install-backend.sh"
ok "$GOOS / $GOARCH"

# ── 2. 安装系统包 ──
log "安装系统包 (mysql, nginx, certbot, ufw)…"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq \
  mysql-server nginx certbot python3-certbot-nginx rsync curl ca-certificates ufw
ok "系统包就绪"

# ── 3. 防火墙 ──
if [[ "$SKIP_FIREWALL" -eq 0 ]]; then
  log "配置防火墙 (22/80/443)…"
  ufw --force reset >/dev/null 2>&1 || true
  ufw default deny incoming
  ufw default allow outgoing
  ufw allow 22/tcp
  ufw allow 80/tcp
  ufw allow 443/tcp
  ufw --force enable >/dev/null 2>&1
  ok "防火墙就绪"
fi

# ── 4. 下载二进制 ──
log "下载 gokych (${RELEASE}, ${GOOS}/${GOARCH})…"
TMPDIR_DL="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_DL"' EXIT

# 选下载源
dl_base() {
  case "$HOST" in
    github)  echo "https://github.com/${GH_REPO}" ;;
    gitcode) echo "https://gitcode.com/${GC_REPO}" ;;
    auto)
      if curl -fsS --max-time 5 -o /dev/null "https://github.com/${GH_REPO}/releases/latest"; then
        echo "https://github.com/${GH_REPO}"
      elif curl -fsS --max-time 5 -o /dev/null "https://gitcode.com/${GC_REPO}/releases/latest"; then
        warn "GitHub 不通, 回退到 GitCode"
        echo "https://gitcode.com/${GC_REPO}"
      else
        die "GitHub 和 GitCode 都不通, 检查网络或手动 --host= 指定"
      fi
      ;;
    *) die "--host 只能是 github / gitcode / auto" ;;
  esac
}

if [[ "$RELEASE" == "latest" ]]; then
  LATEST_TAG="$(curl -fsSL "$(dl_base)/releases/latest" 2>/dev/null \
    | grep -oE '/tag/[^"]+' | head -1 | sed 's|/tag/||')"
  [[ -z "$LATEST_TAG" ]] && die "找不到 latest tag, 用 --release=vX.Y.Z 显式指定"
  RELEASE="$LATEST_TAG"
  ok "latest = $RELEASE"
fi

BASE="$(dl_base)"
ASSET="${ASSET_PREFIX}-${GOOS}-${GOARCH}"
URL_BASE="${BASE}/releases/download/${RELEASE}"
curl -fsSL -o "${TMPDIR_DL}/${ASSET}"     "${URL_BASE}/${ASSET}"
curl -fsSL -o "${TMPDIR_DL}/${SUMS_FILE}" "${URL_BASE}/${SUMS_FILE}"
ok "下载完成 (${ASSET})"

# 校验 hash (从 SHA256SUMS 里 grep 我们的 asset)
EXPECTED="$(grep -E "[[:space:]]\.?/?${ASSET}\$" "${TMPDIR_DL}/${SUMS_FILE}" | awk '{print $1}' || true)"
if [[ -n "$EXPECTED" ]]; then
  verify_sha256 "${TMPDIR_DL}/${ASSET}" "$EXPECTED"
else
  warn "SHA256SUMS 里找不到 ${ASSET} — 跳过 hash 校验"
fi

# ── 5. 部署目录 + 用户 ──
log "建部署目录和 deploy 用户…"
id deploy >/dev/null 2>&1 || adduser --disabled-password --gecos "" deploy
mkdir -p "${INSTALL_DIR}/bin" "${DATA_DIR}"/{uploads,avatars,settings,plugins,themes,typst}
chown -R deploy:deploy "$INSTALL_DIR"
echo "deploy ALL=(ALL) NOPASSWD: /bin/systemctl restart ${SERVICE_NAME}, /bin/systemctl status ${SERVICE_NAME}" \
  > "/etc/sudoers.d/deploy-${SERVICE_NAME}"
chmod 440 "/etc/sudoers.d/deploy-${SERVICE_NAME}"
ok "目录 + deploy 用户就绪"

# ── 6. 安装二进制 (带回滚) ──
log "装二进制…"
[[ -f "$BIN_PATH" ]] && cp "$BIN_PATH" "${INSTALL_DIR}/bin/gokych.prev"
install -o deploy -g deploy -m 0755 "${TMPDIR_DL}/${ASSET}" "$BIN_PATH"
ok "二进制就位 ($BIN_PATH, $(du -h "$BIN_PATH" | cut -f1))"

# ── 7. .env ──
log "写 /opt/gokych/.env…"
cat > "$ENV_FILE" <<ENV
# Generated by install-all.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ)
# Site: ${SITE_NAME}
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
ADMIN_USERNAME=${ADMIN_USERNAME}
ADMIN_PASSWORD=${ADMIN_PASSWORD}
DATA_DIR=${DATA_DIR}
APP_DOMAIN=${APP_DOMAIN}
PUBLIC_URL=${PUBLIC_URL}
CORS_ALLOWED_ORIGINS=${CORS_ORIGIN}
TRUSTED_PROXIES=127.0.0.1
ENV
chown deploy:deploy "$ENV_FILE"
chmod 600 "$ENV_FILE"
ok ".env (DB_PASSWORD / SESSION_SECRET 已随机生成 48 字符)"

# ── 8. MySQL ──
log "初始化 MySQL…"
mysql -e "CREATE DATABASE IF NOT EXISTS gokych CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
mysql -e "CREATE USER IF NOT EXISTS 'gokych'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD}';"
mysql -e "ALTER USER 'gokych'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD}';"
mysql -e "GRANT ALL PRIVILEGES ON gokych.* TO 'gokych'@'127.0.0.1';"
mysql -e "FLUSH PRIVILEGES;"
ok "MySQL (db=gokych, user=gokych@127.0.0.1)"

# ── 9. systemd unit ──
log "写 gokych.service…"
cat > "$SERVICE_FILE" <<'UNIT'
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
systemctl enable "$SERVICE_NAME"
ok "systemd unit 就位"

# ── 10. nginx (HTTP-only, certbot 之后再加 443 ssl) ──
log "写 nginx (HTTP-only, certbot 自动加 443)…"

# Cloudflare real_ip snippet — restores the real client IP when api.<domain>
# is proxied through Cloudflare (orange cloud). No-op when DNS-only (grey):
# set_real_ip_from only trusts CF ranges, so non-CF clients are unaffected.
# Keep these ranges in sync with https://www.cloudflare.com/ips/ — the
# canonical copy lives at scripts/nginx-cloudflare-realip.conf in the repo
# (duplicated here because install-all.sh runs via `curl | bash` with no
# repo checkout).
mkdir -p /etc/nginx/snippets
cat > /etc/nginx/snippets/cloudflare-realip.conf <<'REALIP'
# IPv4
set_real_ip_from 173.245.48.0/20;
set_real_ip_from 103.21.244.0/22;
set_real_ip_from 103.22.200.0/22;
set_real_ip_from 103.31.4.0/22;
set_real_ip_from 141.101.64.0/18;
set_real_ip_from 108.162.192.0/18;
set_real_ip_from 190.93.240.0/20;
set_real_ip_from 188.114.96.0/20;
set_real_ip_from 197.234.240.0/22;
set_real_ip_from 198.41.128.0/17;
set_real_ip_from 162.158.0.0/15;
set_real_ip_from 104.16.0.0/13;
set_real_ip_from 104.24.0.0/14;
set_real_ip_from 172.64.0.0/13;
set_real_ip_from 131.0.72.0/22;
# IPv6
set_real_ip_from 2400:cb00::/32;
set_real_ip_from 2606:4700::/32;
set_real_ip_from 2803:f800::/32;
set_real_ip_from 2403:b300::/32;
set_real_ip_from 2405:8100::/32;
set_real_ip_from 2a06:98c0::/29;
set_real_ip_from 2c0f:f248::/32;
real_ip_header CF-Connecting-IP;
real_ip_recursive on;
REALIP

cat > "$NGINX_SITE" <<NGINX
# ── ${API_DOMAIN} ── 后端 API + 静态资源 ──
server {
    listen 80;
    listen [::]:80;
    server_name ${API_DOMAIN};

    client_max_body_size 50m;

    # Restore real client IP when api.<domain> is behind Cloudflare (orange
    # cloud); no-op when DNS-only. Snippet written above.
    include /etc/nginx/snippets/cloudflare-realip.conf;

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

# ── ${MAIN_DOMAIN} ── 主域名 301 跳前端 ──
server {
    listen 80;
    listen [::]:80;
    server_name ${MAIN_DOMAIN};
    return 301 https://${FRONTEND_DOMAIN}\$request_uri;
}
NGINX

rm -f /etc/nginx/sites-enabled/default
ln -sf "$NGINX_SITE" "$NGINX_LINK"
nginx -t
systemctl restart nginx
ok "nginx HTTP 就绪"

# ── 11. certbot ──
if [[ "$SKIP_CERTBOT" -eq 0 ]]; then
  log "certbot 签证书 (${API_DOMAIN} + ${MAIN_DOMAIN}, --redirect 强制 HTTPS)…"
  certbot --nginx \
    -d "${API_DOMAIN}" -d "${MAIN_DOMAIN}" \
    --non-interactive --agree-tos -m "${EMAIL}" --redirect
  # IPv6 listen 443 (certbot 只加 IPv4)
  sed -i 's/^    listen 443 ssl;$/&\n    listen [::]:443 ssl;/' "$NGINX_LINK"
  nginx -t
  systemctl reload nginx
  ok "TLS 证书已签 (HTTPS 自动跳转就位)"
else
  warn "跳过 certbot — 你需要自己处理 443 ssl"
fi

# ── 12. 启动 gokych ──
log "启动 gokych…"
systemctl restart "$SERVICE_NAME"
sleep 2
systemctl --no-pager --full status "$SERVICE_NAME" | head -10 || true

# ── 13. 健康检查 ──
log "健康检查…"
sleep 1
if curl -fsS http://127.0.0.1:8000/api/health >/dev/null 2>&1; then
  ok "127.0.0.1:8000/api/health OK"
else
  warn "本地 health 失败 — journalctl -u gokych --no-pager -n 30"
fi

if [[ "$SKIP_CERTBOT" -eq 0 ]]; then
  echo
  log "外网 HTTPS 检查…"
  if curl -fsS "https://${API_DOMAIN}/healthz" >/dev/null 2>&1; then
    ok "https://${API_DOMAIN}/healthz OK"
  else
    warn "外网 health 失败 — DNS 可能还没指过来"
  fi
fi

# ── 14. 备份脚本 (写出来, 用户手动加 crontab) ──
log "写 MySQL 备份脚本 /opt/gokych/bin/backup-mysql.sh…"
cat > /opt/gokych/bin/backup-mysql.sh <<'BACKUP'
#!/bin/bash
# Daily MySQL backup — add to crontab: 30 3 * * * /opt/gokych/bin/backup-mysql.sh
set -euo pipefail
mkdir -p /opt/gokych/data/backups
TS=$(date +%F)
mysqldump gokych | gzip > "/opt/gokych/data/backups/gokych-${TS}.sql.gz"
find /opt/gokych/data/backups -name '*.sql.gz' -mtime +30 -delete
BACKUP
chmod +x /opt/gokych/bin/backup-mysql.sh
chown deploy:deploy /opt/gokych/bin/backup-mysql.sh
ok "备份脚本就位 (/opt/gokych/bin/backup-mysql.sh, 30 天轮转)"

# ── 完成 ──
PUBLIC_IP="$(curl -fsS --max-time 3 https://ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')"

cat <<EOF

${GRN}╔══════════════════════════════════════════════════════════════╗${NC}
${GRN}║  后端部署完成 🚀                                              ║${NC}
${GRN}╚══════════════════════════════════════════════════════════════╝${NC}

${BLU}▶ 接下来部署前端 (EdgeOne Makers)${NC}
  1. EdgeOne Makers 控制台 → 连 GitHub 仓库 (CrossDark/GoKYCH) → 自动构建
  2. 环境变量 (生产):
       NEXT_PUBLIC_API_BASE_URL=https://${API_DOMAIN}
  3. 自定义域名: ${FRONTEND_DOMAIN}

${BLU}▶ DNS 解析${NC}
  ${API_DOMAIN}     A     ${PUBLIC_IP}
  ${MAIN_DOMAIN}    A     ${PUBLIC_IP}
  ${FRONTEND_DOMAIN}  CNAME  EdgeOne Makers 分配的加速域名

${BLU}▶ 访问入口${NC}
  后台:  https://${FRONTEND_DOMAIN}/admin
  登录:  ${ADMIN_USERNAME} / ${ADMIN_PASSWORD}
  站点:  ${SITE_NAME}

${BLU}▶ 凭据备份 (重要!)${NC}
  /opt/gokych/.env 包含 DB_PASSWORD + SESSION_SECRET — chmod 600
  cp /opt/gokych/.env ~/gokych.env.backup

${BLU}▶ 常用命令${NC}
  状态:   systemctl status gokych
  日志:   journalctl -u gokych -f
  重启:   systemctl restart gokych
  升级:   重新跑本脚本
  卸载:   sudo bash \$0 --uninstall

${BLU}▶ MySQL 备份 (加 crontab 启用)${NC}
  sudo crontab -e
  # 加一行:
  30 3 * * * /opt/gokych/bin/backup-mysql.sh
EOF