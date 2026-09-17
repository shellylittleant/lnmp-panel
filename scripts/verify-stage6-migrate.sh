#!/bin/bash
# 两个 Ubuntu 24.04 容器模拟 A（源）B（目标）：配对、预检、全量、增量，断言一致。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/lnmp-panel-linux"
test -x "$BIN" || { echo "请先交叉编译 $BIN"; exit 1; }
export PATH="${HOME}/.orbstack/bin:/usr/local/bin:$PATH"
export DEBIAN_FRONTEND=noninteractive

NET=lnmp-mig6
A=lnmp-mig-a
B=lnmp-mig-b
PFXA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
PFXB=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
PASS='GoodPassw0rd!'

cleanup() {
  docker rm -f "$A" "$B" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup || true
docker network create "$NET"

run_panel() {
  local name="$1" pfx="$2"
  docker run -d --name "$name" --network "$NET" --hostname "$name" \
    -v "$BIN:/usr/local/bin/lnmp-panel:ro" \
    ubuntu:24.04 bash -c '
      set -e
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq
      apt-get install -y -qq ca-certificates curl openssh-client openssh-server rsync nginx sqlite3 >/dev/null
      mkdir -p /run/sshd /root/.ssh
      chmod 700 /root/.ssh
      ssh-keygen -A
      printf "\nPermitRootLogin prohibit-password\nPasswordAuthentication no\nPubkeyAuthentication yes\n" >> /etc/ssh/sshd_config
      /usr/sbin/sshd
      lnmp-panel bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel \
        --listen 0.0.0.0:18443 --prefix '"$pfx"' --password '"'$PASS'"'
      umask 077
      lnmp-panel run --config /etc/lnmp-panel/panel.json >/tmp/panel.log 2>&1
    '
}

run_panel "$B" "$PFXB"
run_panel "$A" "$PFXA"

wait_https() {
  local name="$1" pfx="$2"
  for i in $(seq 1 180); do
    docker exec "$name" curl -sk --max-time 1 "https://127.0.0.1:18443/${pfx}/" >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "WAIT_FAIL $name"
  docker logs "$name" 2>&1 | tail -40 || true
  docker exec "$name" cat /tmp/panel.log 2>/dev/null | tail -40 || true
  return 1
}

wait_https "$A" "$PFXA"
wait_https "$B" "$PFXB"
echo PANELS_UP

login() {
  local name="$1" pfx="$2" cj="$3"
  docker exec "$name" bash -c "
    API=https://127.0.0.1:18443/${pfx}/api/v1
    curl -sk -c $cj -b $cj -H 'Content-Type: application/json' \
      -d '{\"username\":\"admin\",\"password\":\"$PASS\"}' \$API/auth/login >/dev/null
    SESS=\$(curl -sk -c $cj -b $cj \$API/auth/session)
    printf '%s' \"\$SESS\" | sed -n 's/.*\"csrf_token\":\"\\([^\"]*\\)\".*/\\1/p'
  "
}

CSRFA=$(login "$A" "$PFXA" /tmp/cja)
CSRFB=$(login "$B" "$PFXB" /tmp/cjb)
echo "CSRFA_LEN=${#CSRFA} CSRFB_LEN=${#CSRFB}"
test "${#CSRFA}" -gt 10
test "${#CSRFB}" -gt 10

wait_job() {
  local name="$1" pfx="$2" cj="$3" jid="$4"
  for i in $(seq 1 90); do
    ST=$(docker exec "$name" bash -c "curl -sk -c $cj -b $cj https://127.0.0.1:18443/${pfx}/api/v1/jobs/$jid")
    echo "JOB $name $i $ST"
    echo "$ST" | grep -q '"phase":"done"' && return 0
    echo "$ST" | grep -q '"phase":"failed"' && { echo "$ST"; return 1; }
    sleep 2
  done
  echo JOB_TIMEOUT; return 1
}

install_nginx() {
  local name="$1" pfx="$2" cj="$3" csrf="$4"
  INST=$(docker exec "$name" bash -c "
    curl -sk -c $cj -b $cj -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf' \
      -d '{}' https://127.0.0.1:18443/${pfx}/api/v1/env/nginx/install
  ")
  echo "NGINX_INSTALL $name $INST"
  JID=$(printf '%s' "$INST" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
  test -n "$JID"
  wait_job "$name" "$pfx" "$cj" "$JID"
}

install_nginx "$A" "$PFXA" /tmp/cja "$CSRFA"
install_nginx "$B" "$PFXB" /tmp/cjb "$CSRFB"
echo NGINX_OK

# A：建站、写文件、用户模板、证书、库元数据；切 PHP 并确认 index.php 优先
docker exec "$A" bash -c "
  API=https://127.0.0.1:18443/${PFXA}/api/v1
  CSRF='$CSRFA'
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"site_key\":\"demo\",\"name\":\"演示\",\"domains\":[\"demo.test\"]}' \$API/sites | grep -q '\"ok\":true'
  echo unique-site-payload-aaa > /var/www/demo/mark.txt
  printf '%s\n' 'HTML_MARKER' > /var/www/demo/index.html
  printf '%s\n' '<?php echo \"PHP_MARKER\";' > /var/www/demo/index.php
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"type\":\"nginx_rewrite\",\"name\":\"迁移用户模板\",\"content\":\"# USER_TPL_MIGRATE\\n\"}' \$API/templates | grep -q '\"ok\":true'
  PUT=\$(curl -sk -c /tmp/cja -b /tmp/cja -X PUT -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"php_version\":\"8.3\",\"force\":true}' \$API/sites/1)
  echo \"PHP_SWITCH=\$PUT\"
  echo \"\$PUT\" | grep -q '\"ok\":true'
  mkdir -p /var/lib/lnmp-panel/tls/sites/1
  printf '%s\n' '-----BEGIN CERTIFICATE-----' MIGRATE-CERT-MARKER '-----END CERTIFICATE-----' > /var/lib/lnmp-panel/tls/sites/1/fullchain.pem
  printf '%s\n' '-----BEGIN EC PRIVATE KEY-----' MIGRATE-KEY-MARKER '-----END EC PRIVATE KEY-----' > /var/lib/lnmp-panel/tls/sites/1/privkey.pem
  sqlite3 /var/lib/lnmp-panel/panel.db \"INSERT INTO mysql_databases(name, charset, \\\"collate\\\", created_at) VALUES ('migdb','utf8mb4','utf8mb4_0900_ai_ci','2026-01-01T00:00:00Z');\"
"

docker exec "$A" grep -q 'try_files \$uri /index.php' /etc/nginx/sites-available/demo.conf
docker exec "$A" grep -q 'rewrite ^ /index.php last' /etc/nginx/sites-available/demo.conf
docker exec "$A" grep -q 'index index.php' /etc/nginx/sites-available/demo.conf
echo PHP_CONF_OK

docker exec "$A" bash -c 'ls -la /var/www/demo'
# 有 nginx 无 php-fpm 时，/ 应走 index.php（502/404）而不是 index.html（200 HTML_MARKER）
# 不手动 reload：面板 Update 已 TestAndReload。稍等 worker 换配置。
sleep 1
HTTP=$(docker exec "$A" bash -c "curl -sS -D /tmp/idxhdr -o /tmp/idxbody -w '%{http_code}' --connect-timeout 5 -H 'Host: demo.test' http://127.0.0.1/ || echo 000")
BODY=$(docker exec "$A" cat /tmp/idxbody 2>/dev/null || true)
echo "PHP_INDEX_HTTP=$HTTP BODY=$BODY"
docker exec "$A" cat /tmp/idxhdr 2>/dev/null || true
docker exec "$A" tail -5 /var/log/nginx/demo.access.log 2>/dev/null || true
docker exec "$A" tail -10 /var/log/nginx/demo.error.log 2>/dev/null || true
if echo "$BODY" | grep -q HTML_MARKER; then
  echo "PHP_INDEX_HTML_WINS"
  docker exec "$A" cat /etc/nginx/sites-available/demo.conf
  docker exec "$A" sh -c 'ps aux | grep nginx; ls -l /etc/nginx/sites-enabled'
  exit 1
fi
if echo "$BODY" | grep -q PHP_MARKER; then
  echo "PHP_INDEX_STATIC_SOURCE"
  exit 1
fi
echo PHP_INDEX_OK

# B 生成令牌
TOK=$(docker exec "$B" bash -c "
  curl -sk -c /tmp/cjb -b /tmp/cjb -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFB' \
    -d '{}' https://127.0.0.1:18443/${PFXB}/api/v1/migrate/token
")
echo "TOKEN_RESP=$TOK"
TOKEN=$(printf '%s' "$TOK" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
test -n "$TOKEN"
echo TOKEN_OK

CONN=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{\"address\":\"https://lnmp-mig-b:18443/${PFXB}\",\"token\":\"$TOKEN\"}' \
    https://127.0.0.1:18443/${PFXA}/api/v1/migrate/connect
")
echo "CONN=$CONN"
echo "$CONN" | grep -q '"ok":true' || { echo CONNECT_FAIL; docker exec "$B" cat /tmp/panel.log | tail -50; docker exec "$A" cat /tmp/panel.log | tail -50; exit 1; }
echo CONNECT_OK

PF=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:18443/${PFXA}/api/v1/migrate/preflight
")
echo "PRE=$PF"
echo "$PF" | grep -q '"ok":true' || { echo PREFLIGHT_FAIL; exit 1; }
echo "$PF" | grep -q '"ok":false' && { echo PREFLIGHT_NOT_OK; exit 1; }
echo PREFLIGHT_OK

wait_mig() {
  local want="$1"
  for i in $(seq 1 90); do
    ST=$(docker exec "$A" bash -c "curl -sk -c /tmp/cja -b /tmp/cja https://127.0.0.1:18443/${PFXA}/api/v1/migrate/status")
    echo "MIG $i $ST"
    echo "$ST" | grep -q "\"phase\":\"$want\"" && return 0
    echo "$ST" | grep -q '"phase":"failed"' && { echo MIG_FAILED; echo "$ST"; docker exec "$A" cat /tmp/panel.log | tail -40; docker exec "$B" cat /tmp/panel.log | tail -40; return 1; }
    sleep 2
  done
  echo MIG_TIMEOUT; return 1
}

FULL=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{\"mode\":\"full\"}' https://127.0.0.1:18443/${PFXA}/api/v1/migrate/start
")
echo "FULL=$FULL"
echo "$FULL" | grep -q '"ok":true' || { echo FULL_START_FAIL; exit 1; }
wait_mig done
echo FULL_OK

docker exec "$B" test -f /var/www/demo/mark.txt
docker exec "$B" grep -q unique-site-payload-aaa /var/www/demo/mark.txt
docker exec "$B" test -f /etc/nginx/sites-available/demo.conf
docker exec "$B" grep -q 'server_name demo.test' /etc/nginx/sites-available/demo.conf
docker exec "$B" grep -q 'try_files \$uri /index.php' /etc/nginx/sites-available/demo.conf
docker exec "$B" grep -q MIGRATE-CERT-MARKER /var/lib/lnmp-panel/tls/sites/1/fullchain.pem
echo ASSERT_FULL_FILES_OK

docker exec "$A" bash -c 'echo unique-incr-bbb >> /var/www/demo/mark.txt'
INCR=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{\"mode\":\"incr\"}' https://127.0.0.1:18443/${PFXA}/api/v1/migrate/start
")
echo "INCR=$INCR"
echo "$INCR" | grep -q '"ok":true' || { echo INCR_START_FAIL; exit 1; }
wait_mig done
docker exec "$B" grep -q unique-incr-bbb /var/www/demo/mark.txt
echo INCR_OK

docker exec "$B" bash -c "
  API=https://127.0.0.1:18443/${PFXB}/api/v1
  S=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/sites)
  echo \"\$S\" | grep -q demo || { echo SITES_B_FAIL; echo \"\$S\"; exit 1; }
  T=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/templates)
  echo \"\$T\" | grep -q USER_TPL_MIGRATE || { echo TPL_B_FAIL; echo \"\$T\"; exit 1; }
  D=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/mysql/databases)
  echo \"\$D\" | grep -q migdb || { echo DB_B_FAIL; echo \"\$D\"; exit 1; }
"
echo TEMPLATES_DB_OK

# 作废钥
docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:18443/${PFXA}/api/v1/migrate/cancel | grep -q '\"ok\":true'
"
docker exec "$A" bash -c 'test ! -e /var/lib/lnmp-panel/secrets/migrate-key'
docker exec "$B" bash -c 'if test -f /root/.ssh/authorized_keys; then ! grep -q LNMP-PANEL-MIGRATE /root/.ssh/authorized_keys; fi'
echo CANCEL_OK

echo STAGE6_MIGRATE_OK
