#!/usr/bin/env bash
# 4f 块原样跑一次,用 bash -x 看每步实际执行
#
# 模拟 deploy 4f 上下文:用 bash -s 从 stdin 读(就是 rsh LOCAL_MODE 的方式)
# 用 set -x 让每行命令打印
#
# 用法:sudo bash scripts/diag-nginx2.sh
set -u
sudo bash -s <<'REMOTE' 2>&1
set -euo pipefail
set -x
cat >/etc/nginx/sites-available/gokych <<'NGINX'
server {
    listen 80;
    server_name api.kych.net;
    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_set_header Host              $host;
    }
}
NGINX
echo "after cat, file size:"
wc -c /etc/nginx/sites-available/gokych
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
ls -la /etc/nginx/sites-enabled/
nginx -t
echo "nginx -t rc=$?"
systemctl restart nginx
echo "systemctl restart rc=$?"
echo "inner done"
REMOTE
echo "outer after rsh, rc=$?"
