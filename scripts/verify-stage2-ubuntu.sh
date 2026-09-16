#!/bin/bash
# 在 Ubuntu 24.04 容器内跑：bootstrap、登录、安装 Nginx、建站、nginx -t。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl >/dev/null
install -m 0755 /src/bin/lnmp-panel-linux /usr/local/bin/lnmp-panel
PFX=abcdef0123456789abcdef0123456789
lnmp-panel bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel \
  --listen 127.0.0.1:18443 --prefix "$PFX" --password 'GoodPassw0rd!'
echo BOOTSTRAP_OK
lnmp-panel run --config /etc/lnmp-panel/panel.json >/tmp/panel.log 2>&1 &
for i in $(seq 1 50); do
  curl -sk --max-time 1 "https://127.0.0.1:18443/${PFX}/" >/dev/null 2>&1 && break
  sleep 0.2
done
API="https://127.0.0.1:18443/${PFX}/api/v1"
curl -sk -c /tmp/cj -b /tmp/cj -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"GoodPassw0rd!"}' "$API/auth/login"
echo
SESS=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/auth/session")
CSRF=$(printf '%s' "$SESS" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
echo "CSRF_LEN=${#CSRF}"
INST=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/env/nginx/install")
echo "INSTALL=$INST"
JID=$(printf '%s' "$INST" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
for i in $(seq 1 90); do
  ST=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/jobs/$JID")
  echo "JOB $i $ST"
  echo "$ST" | grep -q '"phase":"done"' && break
  echo "$ST" | grep -q '"phase":"failed"' && { echo FAIL_JOB; cat /tmp/panel.log; exit 1; }
  sleep 2
done
echo "$ST" | grep -q '"phase":"done"' || { echo JOB_TIMEOUT; exit 1; }
echo NGINX_BIN=$(command -v nginx || true)
nginx -t
if ! pidof nginx >/dev/null 2>&1; then
  nginx
fi
CREATE=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"site_key":"demo","name":"演示","domains":["demo.test"]}' "$API/sites")
echo "CREATE=$CREATE"
echo "$CREATE" | grep -q '"ok":true' || { echo CREATE_FAIL; exit 1; }
nginx -t
test -f /etc/nginx/sites-available/demo.conf
grep -q 'server_name demo.test' /etc/nginx/sites-available/demo.conf
grep -q 'log_format lnmp_panel' /etc/nginx/conf.d/lnmp-panel.conf
ls -l /etc/nginx/sites-enabled || true
ps aux | grep '[n]ginx' || true
(command -v ss >/dev/null && ss -lnt) || netstat -lnt 2>/dev/null || true
nginx || true
code=$(curl -sS -o /tmp/body -w '%{http_code}' --connect-timeout 3 -H 'Host: demo.test' http://127.0.0.1/ || echo curl_fail)
echo "HTTP_HOST=$code"
cat /tmp/body 2>/dev/null || true
test "$code" = "200"
# 手改漂移
echo '# drift' >> /etc/nginx/sites-available/demo.conf
ID=$(printf '%s' "$CREATE" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
DET=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/sites/$ID")
echo "DET=$DET"
echo "$DET" | grep -q '"drifted":true' || { echo DRIFT_FAIL; exit 1; }
echo STAGE2_UBUNTU_OK
