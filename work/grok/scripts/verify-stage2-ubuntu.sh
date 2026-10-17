#!/bin/bash
# 只做用户会做的动作。不手动启动 nginx。面板必须在 umask 077 下运行。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl >/dev/null
install -m 0755 /src/bin/lnmp-panel-linux /usr/local/bin/lnmp-panel
PFX=abcdef0123456789abcdef0123456789
lnmp-panel bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel \
  --listen 127.0.0.1:18443 --prefix "$PFX" --password 'GoodPassw0rd!'
echo BOOTSTRAP_OK
umask 077
lnmp-panel run --config /etc/lnmp-panel/panel.json >/tmp/panel.log 2>&1 &
PANEL_PID=$!
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
echo "$ST" | grep -q '"phase":"done"' || { echo JOB_TIMEOUT; cat /tmp/panel.log; exit 1; }
CREATE=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"site_key":"demo","name":"演示","domains":["demo.test"]}' "$API/sites")
echo "CREATE=$CREATE"
echo "$CREATE" | grep -q '"ok":true' || { echo CREATE_FAIL; cat /tmp/panel.log; exit 1; }
test -f /etc/nginx/sites-available/demo.conf
grep -q 'server_name demo.test' /etc/nginx/sites-available/demo.conf
grep -q 'log_format lnmp_panel' /etc/nginx/conf.d/lnmp-panel.conf
grep -q '/var/www/.acme-challenge/' /etc/nginx/sites-available/demo.conf
DIRMODE=$(stat -c '%a' /var/www/demo)
FILEMODE=$(stat -c '%a' /var/www/demo/index.html)
echo "DIRMODE=$DIRMODE FILEMODE=$FILEMODE"
test "$DIRMODE" = "755"
test "$FILEMODE" = "644"
# 面板必须已经把 nginx 拉起来，脚本不得执行 nginx
code=$(curl -sS -o /tmp/body -w '%{http_code}' --connect-timeout 5 -H 'Host: demo.test' http://127.0.0.1/ || echo curl_fail)
echo "HTTP_HOST=$code"
cat /tmp/body 2>/dev/null || true
test "$code" = "200"
# ACME 路径 www-data 可读
install -d -m 0755 /var/www/.acme-challenge
echo 'challenge-ok' >/var/www/.acme-challenge/tok
chmod 0644 /var/www/.acme-challenge/tok
acme=$(curl -sS -o /tmp/acme -w '%{http_code}' --connect-timeout 5 -H 'Host: demo.test' \
  http://127.0.0.1/.well-known/acme-challenge/tok || echo fail)
echo "ACME=$acme $(cat /tmp/acme 2>/dev/null || true)"
test "$acme" = "200"
grep -q challenge-ok /tmp/acme
# 注入域名必须拒绝
BAD=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"site_key":"evil","name":"x","domains":["evil.test { }"]}' "$API/sites")
echo "BAD=$BAD"
echo "$BAD" | grep -q '"ok":false' || { echo INJECT_NOT_REJECTED; exit 1; }
# 手改漂移
echo '# drift' >> /etc/nginx/sites-available/demo.conf
ID=$(printf '%s' "$CREATE" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
DET=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/sites/$ID")
echo "DET=$DET"
echo "$DET" | grep -q '"drifted":true' || { echo DRIFT_FAIL; exit 1; }

kill "$PANEL_PID" 2>/dev/null || true
wait "$PANEL_PID" 2>/dev/null || true
lnmp-panel run --config /etc/lnmp-panel/panel.json >>/tmp/panel.log 2>&1 &
ok=0
for i in 1 2 3; do
  code=$(curl -sk --max-time 1 -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"GoodPassw0rd!"}' "$API/auth/login" || true)
  echo "RESTART_LOGIN_$i=$code"
  if [[ "$code" == "200" ]]; then
    ok=1
    break
  fi
  sleep 1
done
test "$ok" = "1" || { echo RESTART_LOGIN_FAIL; cat /tmp/panel.log; exit 1; }
echo RESTART_LOGIN_OK
echo STAGE2_UBUNTU_OK
