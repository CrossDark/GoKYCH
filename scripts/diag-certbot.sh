#!/usr/bin/env bash
# 跑 4g 块:certbot 签证书
# 模拟 deploy 4g body,看 certbot 是不是真在跑
#
# 用法:sudo bash scripts/diag-certbot.sh
set -u
API_DOMAIN="api.kych.net"
MAIN_DOMAIN="kych.net"
EMAIL="liuhanbo333@icloud.com"
export API_DOMAIN MAIN_DOMAIN EMAIL

echo "===== 跑 certbot (类似 deploy 4g body) ====="
echo "API_DOMAIN=$API_DOMAIN"
echo "MAIN_DOMAIN=$MAIN_DOMAIN"
echo "EMAIL=$EMAIL"
echo
echo "===== 确认 nginx 状态 ====="
systemctl status nginx --no-pager -l 2>&1 | head -10 || true
echo
echo "===== 确认 80 端口在 listen ====="
ss -tlnp 2>/dev/null | grep -E ':80\b' || netstat -tlnp 2>/dev/null | grep -E ':80\b' || true
echo
echo "===== 模拟 80 端口响应 (HTTP-01 challenge 路径) ====="
curl -sv http://127.0.0.1/.well-known/acme-challenge/test-please 2>&1 | head -10 || true
echo
echo "===== 跑 certbot --nginx (无 --redirect,免得动 nginx) ====="
echo "  注:首次会注册 ACME 账号,需要 30-60s,有时 90s+"
echo
time certbot --nginx \
  -d "$API_DOMAIN" -d "$MAIN_DOMAIN" \
  --non-interactive --agree-tos -m "$EMAIL" 2>&1
echo
echo "===== certbot rc=$? ====="
echo
echo "===== 看 nginx config 是不是被 certbot 改过 ====="
head -20 /etc/nginx/sites-enabled/gokych
echo
echo "===== /etc/letsencrypt/live/ ====="
ls -la /etc/letsencrypt/live/ 2>&1 || true
