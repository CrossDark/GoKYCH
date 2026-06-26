#!/usr/bin/env bash
# 一次性诊断:看 /etc/nginx/sites-available/gokych 是否被写出来,
# 4f 那块的 7 步(写文件 / rm default / ln / nginx -t / restart)哪一步挂
#
# 用法:sudo bash scripts/diag-nginx.sh
set -u
echo "=== /etc/nginx/sites-available/gokych 是否存在 ==="
ls -la /etc/nginx/sites-available/gokych 2>&1 || true
echo
echo "=== 如果存在,内容 ==="
cat /etc/nginx/sites-available/gokych 2>&1 || true
echo
echo "=== /etc/nginx/sites-enabled/ ==="
ls -la /etc/nginx/sites-enabled/
echo
echo "=== 跑 4f 的 7 步,看哪步爆 ==="
sudo bash -c '
set -euo pipefail
echo "step 1: 写文件 (heredoc)"
cat >/etc/nginx/sites-available/gokych <<NGINX_TEST
test $(date)
NGINX_TEST
echo "step 1 ok"
echo
echo "step 2: ls 文件"
ls -la /etc/nginx/sites-available/gokych
echo
echo "step 3: rm default"
rm -f /etc/nginx/sites-enabled/default
echo "step 3 ok rc=$?"
echo
echo "step 4: ln -sf gokych"
ln -sf /etc/nginx/sites-available/gokych /etc/nginx/sites-enabled/
echo "step 4 ok rc=$?"
echo
echo "step 5: ls sites-enabled"
ls -la /etc/nginx/sites-enabled/
echo
echo "step 6: nginx -t"
nginx -t
echo "step 6 ok rc=$?"
echo
echo "step 7: systemctl restart nginx"
systemctl restart nginx
echo "step 7 ok rc=$?"
'
echo
echo "=== 诊断完成 ==="
