#!/bin/bash
# 两个 Ubuntu 24.04 容器：A/B 面板端口与 SSH 端口不同，两边各装 MySQL。
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
PORTA=18443
PORTB=28443
SSHA=22
SSHB=2222
PASS='GoodPassw0rd!'

cleanup() {
  docker rm -f "$A" "$B" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup || true
docker network create "$NET"

run_panel() {
  local name="$1" pfx="$2" listen="$3" sshp="$4"
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
      printf "\nPort '"$sshp"'\nPermitRootLogin prohibit-password\nPasswordAuthentication no\nPubkeyAuthentication yes\n" >> /etc/ssh/sshd_config
      /usr/sbin/sshd
      lnmp-panel bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel \
        --listen 0.0.0.0:'"$listen"' --prefix '"$pfx"' --password '"'$PASS'"'
      umask 077
      lnmp-panel run --config /etc/lnmp-panel/panel.json >/tmp/panel.log 2>&1
    '
}

run_panel "$B" "$PFXB" "$PORTB" "$SSHB"
run_panel "$A" "$PFXA" "$PORTA" "$SSHA"

wait_https() {
  local name="$1" pfx="$2" port="$3"
  for i in $(seq 1 180); do
    docker exec "$name" curl -sk --max-time 1 "https://127.0.0.1:${port}/${pfx}/" >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "WAIT_FAIL $name"
  docker exec "$name" cat /tmp/panel.log 2>/dev/null | tail -40 || true
  return 1
}

wait_https "$A" "$PFXA" "$PORTA"
wait_https "$B" "$PFXB" "$PORTB"
echo PANELS_UP

login() {
  local name="$1" pfx="$2" port="$3" cj="$4"
  docker exec "$name" bash -c "
    API=https://127.0.0.1:${port}/${pfx}/api/v1
    curl -sk -c $cj -b $cj -H 'Content-Type: application/json' \
      -d '{\"username\":\"admin\",\"password\":\"$PASS\"}' \$API/auth/login >/dev/null
    SESS=\$(curl -sk -c $cj -b $cj \$API/auth/session)
    printf '%s' \"\$SESS\" | sed -n 's/.*\"csrf_token\":\"\\([^\"]*\\)\".*/\\1/p'
  "
}

CSRFA=$(login "$A" "$PFXA" "$PORTA" /tmp/cja)
CSRFB=$(login "$B" "$PFXB" "$PORTB" /tmp/cjb)
echo "CSRFA_LEN=${#CSRFA} CSRFB_LEN=${#CSRFB}"
test "${#CSRFA}" -gt 10
test "${#CSRFB}" -gt 10

wait_job() {
  local name="$1" pfx="$2" port="$3" cj="$4" jid="$5"
  for i in $(seq 1 180); do
    ST=$(docker exec "$name" bash -c "curl -sk -c $cj -b $cj https://127.0.0.1:${port}/${pfx}/api/v1/jobs/$jid")
    echo "JOB $name $i $ST"
    echo "$ST" | grep -q '"phase":"done"' && return 0
    echo "$ST" | grep -q '"phase":"failed"' && { echo "$ST"; docker exec "$name" tail -30 /tmp/panel.log; return 1; }
    sleep 2
  done
  echo JOB_TIMEOUT; return 1
}

post_job() {
  local name="$1" pfx="$2" port="$3" cj="$4" csrf="$5" path="$6"
  INST=$(docker exec "$name" bash -c "
    curl -sk -c $cj -b $cj -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $csrf' \
      -d '{}' https://127.0.0.1:${port}/${pfx}/api/v1$path
  ")
  echo "POST $name $path $INST"
  JID=$(printf '%s' "$INST" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
  test -n "$JID"
  wait_job "$name" "$pfx" "$port" "$cj" "$JID"
}

post_job "$A" "$PFXA" "$PORTA" /tmp/cja "$CSRFA" /env/nginx/install
post_job "$B" "$PFXB" "$PORTB" /tmp/cjb "$CSRFB" /env/nginx/install
echo NGINX_OK

post_job "$A" "$PFXA" "$PORTA" /tmp/cja "$CSRFA" /env/mysql/install &
PA=$!
post_job "$B" "$PFXB" "$PORTB" /tmp/cjb "$CSRFB" /env/mysql/install &
PB=$!
wait $PA
wait $PB
echo MYSQL_OK

# A：建站、PHP 优先、防火墙表单、库和用户
docker exec "$A" bash -c "
  API=https://127.0.0.1:${PORTA}/${PFXA}/api/v1
  CSRF='$CSRFA'
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"site_key\":\"demo\",\"name\":\"演示\",\"domains\":[\"demo.test\"]}' \$API/sites | grep -q '\"ok\":true'
  echo unique-site-payload-aaa > /var/www/demo/mark.txt
  printf '%s\n' 'HTML_MARKER' > /var/www/demo/index.html
  printf '%s\n' '<?php echo \"PHP_MARKER\";' > /var/www/demo/index.php
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"type\":\"nginx_rewrite\",\"name\":\"迁移用户模板\",\"content\":\"# USER_TPL_MIGRATE\\n\"}' \$API/templates | grep -q '\"ok\":true'
  curl -sk -c /tmp/cja -b /tmp/cja -X PUT -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"php_version\":\"8.3\",\"force\":true}' \$API/sites/1 | grep -q '\"ok\":true'
  mkdir -p /var/lib/lnmp-panel/tls/sites/1
  printf '%s\n' '-----BEGIN CERTIFICATE-----' MIGRATE-CERT-MARKER '-----END CERTIFICATE-----' > /var/lib/lnmp-panel/tls/sites/1/fullchain.pem
  printf '%s\n' '-----BEGIN EC PRIVATE KEY-----' MIGRATE-KEY-MARKER '-----END EC PRIVATE KEY-----' > /var/lib/lnmp-panel/tls/sites/1/privkey.pem
  curl -sk -c /tmp/cja -b /tmp/cja -X PUT -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"ssh_port\":22,\"panel_port\":${PORTA},\"input_policy\":\"drop\",\"public_http\":true}' \$API/firewall | grep -q '\"ok\":true'
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"name\":\"migdb\"}' \$API/mysql/databases | grep -q '\"ok\":true'
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"user\":\"migu\",\"host\":\"localhost\",\"password\":\"GoodPass1\",\"databases\":[\"migdb\"]}' \$API/mysql/users | grep -q '\"ok\":true'
"

docker exec "$A" grep -q 'rewrite ^ /index.php last' /etc/nginx/sites-available/demo.conf
sleep 1
HTTP=$(docker exec "$A" bash -c "curl -sS -o /tmp/idxbody -w '%{http_code}' --connect-timeout 5 -H 'Host: demo.test' http://127.0.0.1/ || echo 000")
BODY=$(docker exec "$A" cat /tmp/idxbody 2>/dev/null || true)
echo "PHP_INDEX_HTTP=$HTTP"
echo "$BODY" | grep -q HTML_MARKER && { echo PHP_INDEX_HTML_WINS; exit 1; }
echo PHP_INDEX_OK

TOK=$(docker exec "$B" bash -c "
  curl -sk -c /tmp/cjb -b /tmp/cjb -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFB' \
    -d '{}' https://127.0.0.1:${PORTB}/${PFXB}/api/v1/migrate/token
")
echo "TOKEN_RESP=$TOK"
TOKEN=$(printf '%s' "$TOK" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
test -n "$TOKEN"

CONN=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{\"address\":\"https://lnmp-mig-b:${PORTB}/${PFXB}\",\"token\":\"$TOKEN\"}' \
    https://127.0.0.1:${PORTA}/${PFXA}/api/v1/migrate/connect
")
echo "CONN=$CONN"
echo "$CONN" | grep -q '"ok":true' || { echo CONNECT_FAIL; docker exec "$A" tail -40 /tmp/panel.log; docker exec "$B" tail -40 /tmp/panel.log; exit 1; }
echo "$CONN" | grep -q "\"ssh_port\":${SSHB}" || echo "WARN_SSH_PORT $CONN"
echo CONNECT_OK

PF=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:${PORTA}/${PFXA}/api/v1/migrate/preflight
")
echo "PRE=$PF"
echo "$PF" | grep -q '"ok":true' || { echo PREFLIGHT_FAIL; exit 1; }
echo PREFLIGHT_OK

wait_mig() {
  local want="$1"
  for i in $(seq 1 120); do
    ST=$(docker exec "$A" bash -c "curl -sk -c /tmp/cja -b /tmp/cja https://127.0.0.1:${PORTA}/${PFXA}/api/v1/migrate/status")
    echo "MIG $i $ST"
    echo "$ST" | grep -q "\"phase\":\"$want\"" && return 0
    echo "$ST" | grep -q '"phase":"failed"' && { echo MIG_FAILED; echo "$ST"; docker exec "$A" tail -40 /tmp/panel.log; docker exec "$B" tail -40 /tmp/panel.log; return 1; }
    sleep 2
  done
  echo MIG_TIMEOUT; return 1
}

start_mig() {
  local mode="$1"
  docker exec "$A" bash -c "
    curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
      -d '{\"mode\":\"$mode\",\"confirm_replace\":true}' https://127.0.0.1:${PORTA}/${PFXA}/api/v1/migrate/start
  "
}

FULL=$(start_mig full)
echo "FULL=$FULL"
echo "$FULL" | grep -q '"ok":true' || { echo FULL_START_FAIL; exit 1; }
wait_mig done
echo FULL_OK

docker exec "$B" test -f /var/www/demo/mark.txt
docker exec "$B" grep -q unique-site-payload-aaa /var/www/demo/mark.txt
docker exec "$B" test -f /etc/nginx/sites-available/demo.conf
docker exec "$B" grep -q 'server_name demo.test' /etc/nginx/sites-available/demo.conf
echo ASSERT_FULL_FILES_OK

# S1：B 开机防火墙不得含 A 的面板端口
docker exec "$B" bash -c "! grep -q '${PORTA}' /etc/lnmp-panel/nftables.conf"
echo FIREWALL_BOOT_OK

docker exec "$A" bash -c 'echo unique-incr-bbb >> /var/www/demo/mark.txt'
INCR=$(start_mig incr)
echo "INCR=$INCR"
echo "$INCR" | grep -q '"ok":true' || { echo INCR_START_FAIL; exit 1; }
wait_mig done
docker exec "$B" grep -q unique-incr-bbb /var/www/demo/mark.txt
echo INCR_OK

docker exec "$B" bash -c "
  API=https://127.0.0.1:${PORTB}/${PFXB}/api/v1
  S=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/sites)
  echo \"\$S\" | grep -q demo || { echo SITES_B_FAIL; echo \"\$S\"; exit 1; }
  T=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/templates)
  echo \"\$T\" | grep -q USER_TPL_MIGRATE || { echo TPL_B_FAIL; echo \"\$T\"; exit 1; }
  D=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/mysql/databases)
  echo \"DBS=\$D\"
  echo \"\$D\" | grep -q '\"ok\":true' || { echo LNMP_PANEL_LOGIN_FAIL; echo \"\$D\"; exit 1; }
  echo \"\$D\" | grep -q migdb || { echo DB_B_FAIL; echo \"\$D\"; exit 1; }
  U=\$(curl -sk -c /tmp/cjb -b /tmp/cjb \$API/mysql/users)
  echo \"USERS=\$U\"
  echo \"\$U\" | grep -q migu || { echo USER_B_FAIL; echo \"\$U\"; exit 1; }
"
echo TEMPLATES_DB_OK

# S2-b：用迁移过来的密码真登录；面板账号仍能连
docker exec "$B" mysql -umigu -pGoodPass1 -e 'select 1' migdb >/tmp/migu.out
grep -q 1 /tmp/migu.out || { echo MIGU_LOGIN_FAIL; cat /tmp/migu.out; exit 1; }
echo MIGU_LOGIN_OK
docker exec "$B" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -N -e 'select 1' | grep -q 1
echo LNMP_PANEL_MYSQL_OK

docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:${PORTA}/${PFXA}/api/v1/migrate/cancel | grep -q '\"ok\":true'
"
docker exec "$A" bash -c 'test ! -e /var/lib/lnmp-panel/secrets/migrate-key'
echo CANCEL_OK

echo STAGE6_MIGRATE_OK
