#!/usr/bin/env bash
# 4f 完整 + 4g certbot 一次跑,看 deploy 是不是真卡在 4g
#
# 用法:bash scripts/diag-4fg.sh
set -u
API_DOMAIN="api.kych.net"
MAIN_DOMAIN="kych.net"
EO_DOMAIN="eo.kych.net"
EMAIL="liuhanbo333@icloud.com"
export API_DOMAIN MAIN_DOMAIN EO_DOMAIN EMAIL

# 1. 重写完整 4f nginx conf(跟 deploy 4f body 一致)
echo "===== step 1: 写完整 4f nginx.conf ====="
bash -s <<REMOTE
set -euo pipefail
cat >/etc/nginx/sites-available/gokych <<'NGINX'
# ─ ${API_DOMAIN} ─ 后端 API + 静态资源 ─
server {
    listen 80;
    server_name ${API_DOMAIN};
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
# ─ ${MAIN_DOMAIN} ─ 主域名 301 跳转到 EdgeOne Makers 部署的前端 ─
server {
    listen 80;
    server_name ${MAIN_DOMAIN};
    return 301 https://${EO_DOMAIN}\$request_uri;
}
NGINX
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
nginx -t
systemctl restart nginx
echo "===== 4f done ====="
REMOTE
echo
echo "===== step 2: 跑 certbot (deploy 4g) ====="
echo "  (certbot 首次会注册 ACME 账号 + HTTP-01 验证,通常 30-90s)"
time certbot --nginx \
  -d "$API_DOMAIN" -d "$MAIN_DOMAIN" \
  --non-interactive --agree-tos -m "$EMAIL" --redirect 2>&1
echo "===== certbot rc=$? ====="
echo
echo "===== step 3: 看结果 ====="
echo "--- /etc/letsencrypt/live/ ---"
ls -la /etc/letsencrypt/live/ 2>&1 || true
echo
echo "--- gokych 是不是被 certbot 改过 (前 30 行) ---"
head -30 /etc/nginx/sites-enabled/gokych
echo
echo "===== done ====="
