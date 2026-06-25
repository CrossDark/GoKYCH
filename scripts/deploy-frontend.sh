#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# gokych 前端一键部署脚本 — Next.js standalone → Ubuntu VM
#
# 在开发机本地构建 Next.js standalone 产物，打成 tar 推到 VM，
# 解压到 /opt/next-server，重启 next-server systemd。
# nginx 的 eo.kych.net server 块由 deploy-backend.sh 预先配好，
# 本脚本只管 Next.js 本身。
#
# 用法:
#   ./scripts/deploy-frontend.sh                 # 首次（装 Node + systemd）
#   ./scripts/deploy-frontend.sh --update        # 仅更新产物
#
# env var:
#   REMOTE_HOST    远端 VM IP / 域名（默认同 deploy-backend.sh 的值）
#   REMOTE_USER    SSH 用户（默认 root）
#   REMOTE_PORT    SSH 端口（默认 22）
#   EO_DOMAIN      前端域名（默认 eo.kych.net）用于 API_BASE_URL
#   API_DOMAIN     后端域名（默认 api.kych.net）SSR 回环其实用 127.0.0.1
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

RED='\033[0;31m'; GRN='\033[0;32m'; YEL='\033[0;33m'; BLU='\033[0;34m'; NC='\033[0m'
log()  { printf "${BLU}==>${NC} %s\n" "$*"; }
ok()   { printf "${GRN}✓${NC} %s\n" "$*"; }
warn() { printf "${YEL}!${NC} %s\n" "$*" >&2; }
die()  { printf "${RED}✗${NC} %s\n" "$*" >&2; exit 1; }

UPDATE_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --update|-u) UPDATE_ONLY=1; shift ;;
    --help|-h)   sed -n '2,20p' "$0"; exit 0 ;;
    *) die "unknown arg: $1" ;;
  esac
done

[[ -z "${REMOTE_HOST:-}" ]] && { read -rp "  远端 VM IP / 域名: " REMOTE_HOST; [[ -n "$REMOTE_HOST" ]] || die "必填"; }
REMOTE_USER="${REMOTE_USER:-root}"
REMOTE_PORT="${REMOTE_PORT:-22}"
EO_DOMAIN="${EO_DOMAIN:-eo.kych.net}"
API_DOMAIN="${API_DOMAIN:-api.kych.net}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WEB_DIR="$REPO_ROOT/web"

rsh()  { ssh -p "$REMOTE_PORT" "${REMOTE_USER}@$REMOTE_HOST" "$@"; }
rscp() { scp -P "$REMOTE_PORT" "$1" "${REMOTE_USER}@$REMOTE_HOST:$2"; }

# ── 1. 本地构建 ──
log "本地构建 Next.js standalone…"
(
  cd "$WEB_DIR"
  npm ci --no-audit --no-fund
  npm run build
)
ok "build 完成"

# ── 2. 打包 standalone 产物 ──
log "打包 tar…"
TAR="/tmp/next-server-$(date +%s).tgz"
(
  cd "$WEB_DIR/.next/standalone"
  tar -czf "$TAR" \
    . \
    -C "$WEB_DIR" .next/static \
    -C "$WEB_DIR" public 2>/dev/null || true
)
ok "tar: $(du -h "$TAR" | cut -f1)"

# ── 3. 上传 ──
log "上传到 VM…"
rscp "$TAR" /tmp/next-server.tgz
rm -f "$TAR"

# ── 4. 首次：装 Node.js 20 + 建 nextjs 用户 + systemd ──
if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  log "远端：安装 Node.js 20…"
  rsh bash -s <<'REMOTE'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
if ! command -v node >/dev/null 2>&1 || [[ "$(node -v 2>/dev/null)" != v20* ]]; then
  curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
  apt-get install -y -qq nodejs
fi
node -v
id nextjs >/dev/null 2>&1 || useradd --system --home-dir /opt/next-server --shell /usr/sbin/nologin nextjs
REMOTE
  ok "Node.js 就绪"

  log "远端：写 next-server.service…"
  rsh bash -s <<REMOTE
set -euo pipefail
cat >/etc/systemd/system/next-server.service <<UNIT
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
UNIT
systemctl daemon-reload
systemctl enable next-server
REMOTE
  ok "next-server.service 就位"
fi

# ── 5. 解压 + 重启 ──
log "远端：部署产物…"
rsh bash -s <<'REMOTE'
set -euo pipefail
# 回滚保险
if [ -d /opt/next-server/server.js ]; then
  cp -a /opt/next-server /opt/next-server.prev
fi
rm -rf /opt/next-server/*
tar -xzf /tmp/next-server.tgz -C /opt/next-server
# standalone 把 .next/static 放在 ./.next/static — tar 已包含
# public 同理
chown -R nextjs:nextjs /opt/next-server
rm -f /tmp/next-server.tgz
REMOTE
ok "产物就位（回滚: cp /opt/next-server.prev /opt/next-server）"

log "重启 next-server…"
rsh "systemctl restart next-server"
sleep 3
rsh "systemctl --no-pager --full status next-server | head -12 || true"

# ── 6. 验证 ──
log "本地验证…"
if rsh "curl -fsS http://127.0.0.1:3000/ -o /dev/null" 2>/dev/null; then
  ok "127.0.0.1:3000 OK"
else
  warn "本地访问失败 — journalctl -u next-server"
  rsh "journalctl -u next-server --no-pager -n 20" || true
fi
echo

if [[ "$UPDATE_ONLY" -eq 0 ]]; then
  log "外网验证…"
  curl -fsS "https://${EO_DOMAIN}/" -o /dev/null 2>/dev/null && ok "https://${EO_DOMAIN}/ OK" || \
    warn "外网访问失败 — EdgeOne 可能还没配好，或 DNS 还没指过来"
fi
echo

ok "前端部署完成 🚀"
echo
cat <<NEXT
下一步：
  - 浏览器打开 https://${EO_DOMAIN}/admin，用 admin / admin123 登录
  - EdgeOne 控制台：${EO_DOMAIN} 添加站点加速，回源地址填 ${REMOTE_HOST}
    回源 Host 填 ${EO_DOMAIN}，协议选 HTTPS，缓存策略：
      .next/static/* → 缓存 30 天 immutable
      /api/*         → 不缓存
  - 后续更新: ./scripts/deploy-frontend.sh --update
NEXT