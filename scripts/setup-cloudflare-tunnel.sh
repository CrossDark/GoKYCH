#!/usr/bin/env bash
# scripts/setup-cloudflare-tunnel.sh
#
# 在 VM 上跑：把 api.<domain> 的回源从「CF→origin 公网入站」改成
# 「Cloudflare Tunnel（cloudflared 主动出站）」。
#
# 适用场景：VM 无公网入站（IPv4 是 SNAT 出站专用、IPv6 入站抖动 20s）。
# cloudflared 主动连 CF，CF 经隧道把请求送到 localhost:8000（gokych），
# 绕开 nginx、不需要开 80/443 入站、不依赖 IPv6。
#
# 前置：
#   - 已装 cloudflared（脚本会装）
#   - 能浏览器登录 Cloudflare 授权（tunnel login）
#   - api.<domain> 当前在 CF zone ywda.net
#
# 用法（在 VM 上 sudo 跑）：
#   TUNNEL_DOMAIN=api.ywda.net sudo -E bash setup-cloudflare-tunnel.sh
#
# 跑完会：
#   1. 装 cloudflared
#   2. cloudflared tunnel login（浏览器授权，写 cert.pem）
#   3. cloudflared tunnel create gokych（写 credentials json）
#   4. 生成 /etc/cloudflared/config.yml（api.<domain> → http://localhost:8000）
#   5. cloudflared tunnel route dns gokych api.<domain>
#      （自动把 api 的 DNS 改成 CNAME → <tunnel>.cfargotunnel.com，
#       原来的 A/AAAA 记录会被替代，不再需要）
#   6. 装 systemd service 并启动
#
# 之后 CF Dashboard 里 api.<domain> 是一条 CNAME 指向隧道；A/AAAA 可删。
# gokych 的 TrustedProxies=127.0.0.1 仍有效（cloudflared 从 127.0.0.1 连），
# cloudflared 会带 CF-Connecting-IP，gokych 读 X-Forwarded-For 拿到真实 IP。
set -euo pipefail

DOMAIN="${TUNNEL_DOMAIN:-api.ywda.net}"
TUNNEL_NAME="gokych"
BACKEND="${TUNNEL_BACKEND:-http://localhost:8000}"
CFG_DIR="/etc/cloudflared"

# ── 1. 装 cloudflared ──
if ! command -v cloudflared >/dev/null 2>&1; then
  echo ">> 安装 cloudflared"
  ARCH=$(dpkg --print-architecture 2>/dev/null || echo amd64)
  case "$ARCH" in
    amd64)  CF_ARCH=amd64 ;;
    arm64)  CF_ARCH=arm64 ;;
    *)      CF_ARCH=amd64 ;;
  esac
  VER="2024.12.2"
  curl -fsSL -o /tmp/cloudflared.deb "https://github.com/cloudflare/cloudflared/releases/download/${VER}/cloudflared-linux-${CF_ARCH}.deb"
  dpkg -i /tmp/cloudflared.deb
  rm -f /tmp/cloudflared.deb
fi
cloudflared --version

# ── 2. 登录（交互，会打印一个 URL 让你在浏览器授权）──
# 授权后会在 ~/.cloudflared/cert.pem 写凭证。用 root 跑 systemd，所以用 root 的 home。
if [ ! -f /root/.cloudflared/cert.pem ]; then
  echo ">> cloudflared tunnel login —— 复制弹出的 URL 到浏览器，选择 ywda.net zone 授权"
  cloudflared tunnel login
fi
[ -f /root/.cloudflared/cert.pem ] || { echo "登录失败：cert.pem 不存在"; exit 1; }

# ── 3. 创建隧道（如不存在）──
TUNNEL_ID="$(cloudflared tunnel list 2>/dev/null | awk -v n="$TUNNEL_NAME" '$2==n{print $1}' || true)"
if [ -z "$TUNNEL_ID" ]; then
  echo ">> cloudflared tunnel create $TUNNEL_NAME"
  cloudflared tunnel create "$TUNNEL_NAME"
  TUNNEL_ID="$(cloudflared tunnel list | awk -v n="$TUNNEL_NAME" '$2==n{print $1}')"
fi
echo ">> 隧道 $TUNNEL_NAME id=$TUNNEL_ID"

# ── 4. 写 config.yml ──
mkdir -p "$CFG_DIR"
CREDS="/root/.cloudflared/${TUNNEL_ID}.json"
[ -f "$CREDS" ] || CREDS="${CFG_DIR}/${TUNNEL_ID}.json"
cat > "${CFG_DIR}/config.yml" <<EOF
tunnel: ${TUNNEL_ID}
credentials-file: ${CREDS}

# 经隧道把 api.<domain> 回源到本机 gokych（绕开 nginx，不需要入站端口）。
# cloudflared 从 127.0.0.1 连 gokych，带 CF-Connecting-IP / X-Forwarded-For；
# gokych TrustedProxies=127.0.0.1 会信它，拿到真实客户端 IP。
ingress:
  - hostname: ${DOMAIN}
    service: ${BACKEND}
    originRequest:
      noTLSVerify: true
      httpHostHeader: ${DOMAIN}
  - service: http_status:404
EOF
echo ">> 配置写入 ${CFG_DIR}/config.yml"

# ── 5. DNS 路由（api.<domain> → 隧道 CNAME，替代 A/AAAA）──
echo ">> cloudflared tunnel route dns $TUNNEL_NAME $DOMAIN"
cloudflared tunnel route dns "$TUNNEL_NAME" "$DOMAIN" || \
  echo "!! DNS 路由失败——手动在 CF Dashboard 给 $DOMAIN 加 CNAME → ${TUNNEL_ID}.cfargotunnel.com（ proxied ）"

# ── 6. systemd service ──
cat > /etc/systemd/system/cloudflared.service <<'UNIT'
[Unit]
Description=Cloudflare Tunnel (gokych)
After=network-online.target gokych.service
Wants=network-online.target

[Service]
Type=notify
ExecStart=/usr/bin/cloudflared --config /etc/cloudflared/config.yml tunnel run
Restart=on-failure
RestartSec=5s
# 限权
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/etc/cloudflared /root/.cloudflared
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now cloudflared
sleep 3
systemctl --no-pager --full status cloudflared | head -15

echo ""
echo "================ 完成 ================"
echo "api.${DOMAIN} 现在经 Cloudflare Tunnel 回源到 ${BACKEND}"
echo "去 CF Dashboard 删掉 api 的 A/AAAA 记录（只留 CNAME → ${TUNNEL_ID}.cfargotunnel.com）"
echo "验证：curl -sI -m 10 https://${DOMAIN}/api/site  （应几百 ms 返回 200）"
