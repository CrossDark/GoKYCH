#!/usr/bin/env bash
# 完整模拟 deploy 4f:outer 设 API_DOMAIN 等,inner 跑 4f body 的真实写法
# 用 set -x 看每步执行结果
#
# 用法:sudo bash scripts/diag-nginx3.sh
set -u
API_DOMAIN="api.kych.net"
MAIN_DOMAIN="kych.net"
EO_DOMAIN="eo.kych.net"
export API_DOMAIN MAIN_DOMAIN EO_DOMAIN

echo "===== outer: API_DOMAIN=$API_DOMAIN ====="
echo "===== 跑 inner (模拟 rsh bash -s) ====="
bash -s <<REMOTE 2>&1
set -euo pipefail
set -x

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

echo "after cat, file size:"
wc -c /etc/nginx/sites-available/gokych
echo
echo "file content head:"
head -5 /etc/nginx/sites-available/gokych
echo "..."
echo "file content tail:"
tail -5 /etc/nginx/sites-available/gokych
echo
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
ls -la /etc/nginx/sites-enabled/
nginx -t
echo "nginx -t rc=$?"
systemctl restart nginx
echo "systemctl restart rc=$?"
echo "inner done"
REMOTE
echo "===== outer after bash -s, rc=$? ====="
