#!/bin/bash
# linux/amd64：验证官方源能装上 MySQL，并由面板建库/导入导出。不手动启动 mysqld。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl >/dev/null
BIN=/src/bin/lnmp-panel-linux-amd64
if [ ! -x "$BIN" ]; then BIN=/src/bin/lnmp-panel-linux; fi
install -m 0755 "$BIN" /usr/local/bin/lnmp-panel
PFX=abcdef0123456789abcdef0123456789
lnmp-panel bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel \
  --listen 127.0.0.1:18443 --prefix "$PFX" --password 'GoodPassw0rd!'
umask 077
lnmp-panel run --config /etc/lnmp-panel/panel.json >/tmp/panel.log 2>&1 &
for i in $(seq 1 50); do
  curl -sk --max-time 1 "https://127.0.0.1:18443/${PFX}/" >/dev/null 2>&1 && break
  sleep 0.2
done
API="https://127.0.0.1:18443/${PFX}/api/v1"
curl -sk -c /tmp/cj -b /tmp/cj -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"GoodPassw0rd!"}' "$API/auth/login"
SESS=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/auth/session")
CSRF=$(printf '%s' "$SESS" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
ENV=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/env/mysql/install")
echo "ENV=$ENV"
JID=$(printf '%s' "$ENV" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
for i in $(seq 1 180); do
  ST=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/jobs/$JID")
  echo "JOB $i $ST"
  echo "$ST" | grep -q '"phase":"done"' && break
  echo "$ST" | grep -q '"phase":"failed"' && { echo FAIL_JOB; cat /tmp/panel.log; exit 1; }
  sleep 2
done
echo "$ST" | grep -q '"phase":"done"' || { echo TIMEOUT; cat /tmp/panel.log; exit 1; }
test -x /usr/sbin/mysqld
/usr/sbin/mysqld --version || true
DB=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{"name":"blog","charset":"utf8mb4"}' "$API/mysql/databases")
echo "DB=$DB"
echo "$DB" | grep -q '"ok":true' || { echo DB_FAIL; cat /tmp/panel.log; exit 1; }
US=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"user":"blogu","host":"localhost","password":"GoodPass1","databases":["blog"]}' "$API/mysql/users")
echo "USER=$US"
echo "$US" | grep -q '"ok":true' || { echo USER_FAIL; cat /tmp/panel.log; exit 1; }
echo 'CREATE TABLE t(id INT PRIMARY KEY); INSERT INTO t VALUES (1);' > /tmp/in.sql
IMP=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H "X-CSRF-Token: $CSRF" \
  -F "file=@/tmp/in.sql;type=text/plain" "$API/mysql/databases/blog/import")
echo "IMP=$IMP"
IJ=$(printf '%s' "$IMP" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
for i in $(seq 1 60); do
  ST=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/jobs/$IJ")
  echo "IMPJOB $i $ST"
  echo "$ST" | grep -q '"phase":"done"' && break
  echo "$ST" | grep -q '"phase":"failed"' && { echo IMP_FAIL; cat /tmp/panel.log; exit 1; }
  sleep 1
done
EXP=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/mysql/databases/blog/export")
echo "EXP=$EXP"
echo "$EXP" | grep -q 'CREATE TABLE' || { echo EXPORT_FAIL; exit 1; }
CNFMODE=$(stat -c '%a' /etc/mysql/mysql.conf.d/lnmp-panel.cnf || echo missing)
echo "CNFMODE=$CNFMODE"
echo STAGE3_MYSQL_AMD64_OK
