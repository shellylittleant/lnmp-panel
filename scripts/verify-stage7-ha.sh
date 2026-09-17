#!/bin/bash
# 双容器热备：配对 → 追平 → 备机启用主模式 → 原主机重启自我保护 → 降级为备再追平。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/lnmp-panel-linux"
test -x "$BIN" || { echo "请先交叉编译 $BIN"; exit 1; }
export PATH="${HOME}/.orbstack/bin:/usr/local/bin:$PATH"
export DEBIAN_FRONTEND=noninteractive

NET=lnmp-ha7
A=lnmp-ha-a
B=lnmp-ha-b
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
      while true; do
        lnmp-panel run --config /etc/lnmp-panel/panel.json >>/tmp/panel.log 2>&1 || true
        echo PANEL_EXIT >>/tmp/panel.log
        sleep 1
      done
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
  docker exec "$name" tail -40 /tmp/panel.log 2>/dev/null || true
  return 1
}

wait_https "$A" "$PFXA" "$PORTA"
wait_https "$B" "$PFXB" "$PORTB"
echo PANELS_UP

login() {
  local name="$1" pfx="$2" port="$3" cj="$4"
  local i csrf
  for i in $(seq 1 10); do
    csrf=$(docker exec "$name" bash -c "
      API=https://127.0.0.1:${port}/${pfx}/api/v1
      curl -sk -c $cj -b $cj -H 'Content-Type: application/json' \
        -d '{\"username\":\"admin\",\"password\":\"$PASS\"}' \$API/auth/login >/dev/null
      SESS=\$(curl -sk -c $cj -b $cj \$API/auth/session)
      printf '%s' \"\$SESS\" | sed -n 's/.*\"csrf_token\":\"\\([^\"]*\\)\".*/\\1/p'
    ")
    if [ "${#csrf}" -gt 10 ]; then
      printf '%s' "$csrf"
      return 0
    fi
    sleep 2
  done
  echo "LOGIN_FAIL $name" >&2
  docker exec "$name" tail -20 /tmp/panel.log >&2 || true
  return 1
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

docker exec "$A" bash -c "
  API=https://127.0.0.1:${PORTA}/${PFXA}/api/v1
  CSRF='$CSRFA'
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"site_key\":\"demo\",\"name\":\"演示\",\"domains\":[\"demo.test\"]}' \$API/sites | grep -q '\"ok\":true'
  printf '%s\n' 'HA_SITE_A' > /var/www/demo/index.html
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H \"X-CSRF-Token: \$CSRF\" \
    -d '{\"name\":\"hadb\"}' \$API/mysql/databases | grep -q '\"ok\":true'
  mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -e \
    'CREATE TABLE hadb.mark (id INT PRIMARY KEY, v VARCHAR(64)); INSERT INTO hadb.mark VALUES (1,\"from-a\");'
"
echo MASTER_DATA_OK

TOK=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/token
")
echo "TOKEN_RESP=$TOK"
TOKEN=$(printf '%s' "$TOK" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
test -n "$TOKEN"

PAIR=$(docker exec "$B" bash -c "
  curl -sk -c /tmp/cjb -b /tmp/cjb -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFB' \
    -d '{\"address\":\"https://lnmp-ha-a:${PORTA}/${PFXA}\",\"token\":\"$TOKEN\"}' \
    https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/pair
")
echo "PAIR=$PAIR"
echo "$PAIR" | grep -q '"ok":true' || { echo PAIR_FAIL; docker exec "$A" tail -50 /tmp/panel.log; docker exec "$B" tail -50 /tmp/panel.log; exit 1; }
echo PAIR_OK
sleep 5

STA=$(docker exec "$A" bash -c "curl -sk -c /tmp/cja -b /tmp/cja https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/status")
STB=$(docker exec "$B" bash -c "curl -sk -c /tmp/cjb -b /tmp/cjb https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/status")
echo "STATUS_A=$STA"
echo "STATUS_B=$STB"
echo "$STA" | grep -q '"role":"master"' || { echo A_NOT_MASTER; exit 1; }
echo "$STB" | grep -q '"role":"standby"' || { echo B_NOT_STANDBY; exit 1; }
docker exec "$B" test -L /etc/nginx/sites-enabled/nginx_catchall_404.conf
echo ROLE_OK

SYNC=$(docker exec "$B" bash -c "
  curl -sk -c /tmp/cjb -b /tmp/cjb -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFB' \
    -d '{}' https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/sync/start
")
echo "SYNC=$SYNC"
echo "$SYNC" | grep -q '"ok":true' || { echo SYNC_START_FAIL; docker exec "$B" tail -50 /tmp/panel.log; exit 1; }

wait_catchup() {
  local name="$1" file="$2" sqlwant="$3"
  for i in $(seq 1 90); do
    if docker exec "$name" test -f "$file" && docker exec "$name" grep -q "$sqlwant" "$file" 2>/dev/null; then
      if docker exec "$name" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -N -e "SELECT v FROM hadb.mark WHERE id=1" 2>/dev/null | grep -q "$sqlwant"; then
        echo "CATCHUP_OK $name $sqlwant"
        return 0
      fi
    fi
    ST=$(docker exec "$name" bash -c "curl -sk -c /tmp/cjb -b /tmp/cjb https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/sync/status" 2>/dev/null || true)
    echo "WAIT_CATCHUP $name $i $ST"
    sleep 3
  done
  echo CATCHUP_FAIL "$name" "$sqlwant"
  docker exec "$name" tail -60 /tmp/panel.log || true
  docker exec "$name" ls -la /var/www/demo 2>/dev/null || true
  return 1
}

# 第一次追平：文件来自 rsync，库来自复制。状态接口在 B。
for i in $(seq 1 90); do
  if docker exec "$B" test -f /var/www/demo/index.html && docker exec "$B" grep -q HA_SITE_A /var/www/demo/index.html; then
    if docker exec "$B" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -N -e "SELECT v FROM hadb.mark WHERE id=1" 2>/dev/null | grep -q from-a; then
      echo CATCHUP1_OK
      break
    fi
  fi
  ST=$(docker exec "$B" bash -c "curl -sk -c /tmp/cjb -b /tmp/cjb https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/sync/status")
  echo "WAIT1 $i $ST"
  if echo "$ST" | grep -q '"detail":' && echo "$ST" | grep -qi '失败\|error\|Error'; then
    echo "$ST"
  fi
  if [ "$i" -eq 90 ]; then
    echo CATCHUP1_FAIL
    docker exec "$B" tail -80 /tmp/panel.log
    docker exec "$A" tail -40 /tmp/panel.log
    exit 1
  fi
  sleep 3
done

# 备机此时应仍是 404
HTTPB=$(docker exec "$B" bash -c "curl -sS -o /tmp/bbody -w '%{http_code}' -H 'Host: demo.test' http://127.0.0.1/ || echo 000")
echo "STANDBY_HTTP=$HTTPB"
echo "$HTTPB" | grep -q 404 || { echo STANDBY_NOT_404; docker exec "$B" cat /tmp/bbody; ls -l /etc/nginx/sites-enabled; exit 1; }
echo STANDBY_404_OK

docker exec "$A" bash -c "
  echo HA_SITE_A2 > /var/www/demo/mark2.txt
  mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -e \"UPDATE hadb.mark SET v='from-a2' WHERE id=1;\"
"
echo "A_HACNF=$(docker exec "$A" cat /etc/mysql/mysql.conf.d/z-lnmp-panel-ha.cnf 2>/dev/null || echo missing)"

for i in $(seq 1 90); do
  ST=$(docker exec "$B" bash -c "curl -sk -c /tmp/cjb -b /tmp/cjb https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/sync/status")
  HASF=0
  docker exec "$B" test -f /var/www/demo/mark2.txt && HASF=1
  SQLV=$(docker exec "$B" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -N -e "SELECT v FROM hadb.mark WHERE id=1" 2>/dev/null || echo FAIL)
  echo "WAIT2 $i file=$HASF sql=$SQLV $ST"
  if [ "$HASF" = 1 ] && echo "$SQLV" | grep -q from-a2; then
    echo CATCHUP2_OK
    break
  fi
  if [ "$i" -eq 90 ]; then
    echo CATCHUP2_FAIL
    docker exec "$B" tail -80 /tmp/panel.log
    docker exec "$B" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -e "SHOW REPLICA STATUS\\G" 2>/dev/null || true
    docker exec "$A" ss -lntp | grep 3306 || true
    exit 1
  fi
  sleep 3
done

PROM=$(docker exec "$B" bash -c "
  curl -sk -c /tmp/cjb -b /tmp/cjb -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFB' \
    -d '{}' https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/promote
")
echo "PROM=$PROM"
echo "$PROM" | grep -q '"ok":true' || { echo PROMOTE_FAIL; docker exec "$B" tail -50 /tmp/panel.log; exit 1; }
STB=$(docker exec "$B" bash -c "curl -sk -c /tmp/cjb -b /tmp/cjb https://127.0.0.1:${PORTB}/${PFXB}/api/v1/ha/status")
echo "STATUS_B_PROM=$STB"
echo "$STB" | grep -q '"role":"master"' || { echo B_PROMOTE_NOT_MASTER; exit 1; }
HTTPB=$(docker exec "$B" bash -c "curl -sS -o /tmp/bbody -w '%{http_code}' -H 'Host: demo.test' http://127.0.0.1/ || echo 000")
echo "B_SITE_HTTP=$HTTPB"
echo "$HTTPB" | grep -q 200 || { echo B_SITE_NOT_200; cat /tmp/bbody; docker exec "$B" ls -l /etc/nginx/sites-enabled; exit 1; }
echo PROMOTE_OK

if ! docker exec "$B" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -e "UPDATE hadb.mark SET v='from-b-master' WHERE id=1;"; then
  docker exec "$B" mysql -uroot -e "UPDATE hadb.mark SET v='from-b-master' WHERE id=1;"
fi
echo B_WRITE_OK

echo RESTART_A
docker exec "$A" bash -c 'killall -TERM lnmp-panel 2>/dev/null || true'
sleep 3
wait_https "$A" "$PFXA" "$PORTA"
CSRFA=$(login "$A" "$PFXA" "$PORTA" /tmp/cja)
echo "CSRFA2_LEN=${#CSRFA}"

STA=$(docker exec "$A" bash -c "curl -sk -c /tmp/cja -b /tmp/cja https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/status")
echo "STATUS_A_AFTER=$STA"
echo "$STA" | grep -q '"degraded":true' || { echo A_NOT_DEGRADED; docker exec "$A" tail -60 /tmp/panel.log; exit 1; }
HTTPA=$(docker exec "$A" bash -c "curl -sS -o /tmp/abody -w '%{http_code}' -H 'Host: demo.test' http://127.0.0.1/ || echo 000")
echo "A_MAINT_HTTP=$HTTPA"
echo "$HTTPA" | grep -q 503 || { echo A_NOT_MAINT; docker exec "$A" cat /tmp/abody; docker exec "$A" ls -l /etc/nginx/sites-enabled; exit 1; }
docker exec "$A" cat /tmp/abody | grep -q '维护' || echo "WARN_MAINT_BODY"
if docker exec "$A" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -e "INSERT INTO hadb.mark VALUES (9,'should-fail');" 2>/tmp/ro.err; then
  echo A_MYSQL_STILL_WRITABLE
  cat /tmp/ro.err || true
  exit 1
fi
echo SELF_PROTECT_OK

DEM=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/demote
")
echo "DEM=$DEM"
echo "$DEM" | grep -q '"ok":true' || { echo DEMOTE_FAIL; docker exec "$A" tail -40 /tmp/panel.log; exit 1; }
STA=$(docker exec "$A" bash -c "curl -sk -c /tmp/cja -b /tmp/cja https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/status")
echo "STATUS_A_DEM=$STA"
echo "$STA" | grep -q '"role":"standby"' || { echo A_NOT_STANDBY; exit 1; }
HTTPA=$(docker exec "$A" bash -c "curl -sS -o /tmp/abody -w '%{http_code}' -H 'Host: demo.test' http://127.0.0.1/ || echo 000")
echo "A_404_HTTP=$HTTPA"
echo "$HTTPA" | grep -q 404 || { echo A_NOT_404; exit 1; }
echo DEMOTE_OK

SYNC2=$(docker exec "$A" bash -c "
  curl -sk -c /tmp/cja -b /tmp/cja -X POST -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRFA' \
    -d '{}' https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/sync/start
")
echo "SYNC2=$SYNC2"
echo "$SYNC2" | grep -q '"ok":true' || { echo A_SYNC_FAIL; docker exec "$A" tail -50 /tmp/panel.log; exit 1; }

docker exec "$B" bash -c 'echo HA_FROM_NEW_MASTER > /var/www/demo/from-b.txt'
if ! docker exec "$B" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -e "UPDATE hadb.mark SET v='from-b2' WHERE id=1;"; then
  docker exec "$B" mysql -uroot -e "UPDATE hadb.mark SET v='from-b2' WHERE id=1;"
fi

for i in $(seq 1 90); do
  if docker exec "$A" test -f /var/www/demo/from-b.txt && docker exec "$A" grep -q HA_FROM_NEW_MASTER /var/www/demo/from-b.txt; then
    if docker exec "$A" mysql --defaults-extra-file=/var/lib/lnmp-panel/secrets/mysql-panel.cnf -N -e "SELECT v FROM hadb.mark WHERE id=1" 2>/dev/null | grep -q from-b2; then
      echo CATCHUP3_OK
      break
    fi
  fi
  ST=$(docker exec "$A" bash -c "curl -sk -c /tmp/cja -b /tmp/cja https://127.0.0.1:${PORTA}/${PFXA}/api/v1/ha/sync/status")
  echo "WAIT3 $i $ST"
  if [ "$i" -eq 90 ]; then
    echo CATCHUP3_FAIL
    docker exec "$A" tail -80 /tmp/panel.log
    docker exec "$B" tail -40 /tmp/panel.log
    exit 1
  fi
  sleep 3
done

# 热备令牌长期有效：对端 peer-role
TOK_M=$(docker exec "$A" bash -c "sqlite3 /var/lib/lnmp-panel/panel.db \"SELECT value FROM settings WHERE key='ha_machine_token';\"")
echo "MACHINE_TOKEN_LEN=${#TOK_M}"
test "${#TOK_M}" -gt 10
PR=$(docker exec "$A" bash -c "
  curl -sk -H 'X-LNMP-Peer-Token: $TOK_M' https://lnmp-ha-b:${PORTB}/${PFXB}/api/v1/ha/peer-role
")
echo "PEER_ROLE=$PR"
echo "$PR" | grep -q '"role":"master"' || { echo PEER_ROLE_FAIL; exit 1; }

echo STAGE7_HA_OK
