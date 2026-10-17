#!/bin/bash
# 只做用户会做的动作。不手动启动 nginx / php-fpm / mysqld。面板必须在 umask 077 下运行。
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

wait_job() {
  local jid="$1" label="$2"
  local st=""
  for i in $(seq 1 180); do
    st=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/jobs/$jid")
    echo "JOB $label $i $st"
    echo "$st" | grep -q '"phase":"done"' && return 0
    echo "$st" | grep -q '"phase":"failed"' && { echo "FAIL_JOB $label"; cat /tmp/panel.log; return 1; }
    sleep 2
  done
  echo "JOB_TIMEOUT $label"
  echo "$st"
  cat /tmp/panel.log
  return 1
}

# Nginx（阶段 2 接口，PHP 站点需要）
INST=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/env/nginx/install")
echo "NGINX_INSTALL=$INST"
JID=$(printf '%s' "$INST" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
wait_job "$JID" nginx

# PHP PPA + MySQL 官方源
ENV=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/env/php/repo")
echo "ENV_INSTALL=$ENV"
EJID=$(printf '%s' "$ENV" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
MYSQL_OK=0
if wait_job "$EJID" env; then
  MYSQL_OK=1
else
  echo "ENV_INSTALL_FAILED 继续探测 PPA 是否已写入"
  ls -l /etc/apt/sources.list.d/ || true
  cat /etc/apt/sources.list.d/*ondrej* 2>/dev/null || true
  cat /etc/apt/sources.list.d/mysql-official.list 2>/dev/null || true
fi

install_php() {
  local ver="$1"
  local out
  out=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
    -H "X-CSRF-Token: $CSRF" -d '{}' "$API/php/versions/${ver}/install")
  echo "PHP_INSTALL_$ver=$out"
  local jid
  jid=$(printf '%s' "$out" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
  wait_job "$jid" "php$ver"
}

install_php 8.2
install_php 8.3

for ver in 8.2 8.3; do
  EXT=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
    -H "X-CSRF-Token: $CSRF" -d '{"name":"mysql"}' "$API/php/versions/${ver}/extensions")
  echo "EXT_$ver=$EXT"
  echo "$EXT" | grep -q '"ok":true' || { echo EXT_FAIL; exit 1; }
done

# 两版本各绑一站
C82=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"site_key":"p82","name":"php82","domains":["php82.test"],"php_version":"8.2"}' "$API/sites")
echo "CREATE82=$C82"
echo "$C82" | grep -q '"ok":true' || { echo CREATE82_FAIL; cat /tmp/panel.log; exit 1; }
C83=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"site_key":"p83","name":"php83","domains":["php83.test"],"php_version":"8.3"}' "$API/sites")
echo "CREATE83=$C83"
echo "$C83" | grep -q '"ok":true' || { echo CREATE83_FAIL; exit 1; }

printf '%s\n' '<?php echo PHP_VERSION;' > /var/www/p82/index.php
printf '%s\n' '<?php echo PHP_VERSION;' > /var/www/p83/index.php
chmod 0644 /var/www/p82/index.php /var/www/p83/index.php
# 用户放置站点文件，不是替面板启动服务
sleep 2
code82=$(curl -sS -o /tmp/b82 -w '%{http_code}' --connect-timeout 5 -H 'Host: php82.test' http://127.0.0.1/ || echo fail)
code83=$(curl -sS -o /tmp/b83 -w '%{http_code}' --connect-timeout 5 -H 'Host: php83.test' http://127.0.0.1/ || echo fail)
echo "HTTP82=$code82 $(cat /tmp/b82 2>/dev/null || true)"
echo "HTTP83=$code83 $(cat /tmp/b83 2>/dev/null || true)"
test "$code82" = "200"
test "$code83" = "200"
grep -q '^8\.2' /tmp/b82
grep -q '^8\.3' /tmp/b83

# php.ini 表单 + 漂移；umask 下片段 644
INI=$(curl -sk -c /tmp/cj -b /tmp/cj -X PUT -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"memory_limit":"384M","upload_max_filesize":"32M","post_max_size":"32M","max_execution_time":"60","display_errors":"Off","opcache.enable":"1","opcache.memory_consumption":"128","opcache.max_accelerated_files":"10000","opcache.validate_timestamps":"0"}' \
  "$API/php/versions/8.3/ini")
echo "INI=$INI"
echo "$INI" | grep -q '"ok":true' || { echo INI_FAIL; exit 1; }
INIMODE=$(stat -c '%a' /etc/php/8.3/fpm/conf.d/99-lnmp-panel.ini)
POOLMODE=$(stat -c '%a' /etc/php/8.3/fpm/pool.d/lnmp-panel.conf)
echo "INIMODE=$INIMODE POOLMODE=$POOLMODE"
test "$INIMODE" = "644"
test "$POOLMODE" = "644"
echo '; drift' >> /etc/php/8.3/fpm/conf.d/99-lnmp-panel.ini
DET=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/php/versions/8.3/ini")
echo "INI_DET=$DET"
echo "$DET" | grep -q '"drifted":true' || { echo INI_DRIFT_FAIL; exit 1; }

# 库名注入
BAD=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{"name":"a;drop"}' "$API/mysql/databases")
echo "BADDB=$BAD"
echo "$BAD" | grep -q '"ok":false' || { echo DB_INJECT_NOT_REJECTED; exit 1; }

ENVSTAT=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/env")
echo "ENVSTAT=$ENVSTAT"
if echo "$ENVSTAT" | grep -q '"mysql_installed":true'; then
  DB=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
    -H "X-CSRF-Token: $CSRF" -d '{"name":"blog","charset":"utf8mb4"}' "$API/mysql/databases")
  echo "DB=$DB"
  echo "$DB" | grep -q '"ok":true' || { echo DB_FAIL; cat /tmp/panel.log; exit 1; }
  US=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
    -H "X-CSRF-Token: $CSRF" \
    -d '{"user":"blogu","host":"localhost","password":"GoodPass1","databases":["blog"]}' "$API/mysql/users")
  echo "USER=$US"
  echo "$US" | grep -q '"ok":true' || { echo USER_FAIL; exit 1; }
  # 导入导出：用户上传 SQL
  echo 'CREATE TABLE t(id INT PRIMARY KEY); INSERT INTO t VALUES (1);' > /tmp/in.sql
  IMP=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H "X-CSRF-Token: $CSRF" \
    -F "file=@/tmp/in.sql;type=text/plain" "$API/mysql/databases/blog/import")
  echo "IMP=$IMP"
  IJ=$(printf '%s' "$IMP" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
  wait_job "$IJ" import
  EXP=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
    -H "X-CSRF-Token: $CSRF" -d '{}' "$API/mysql/databases/blog/export")
  echo "EXP=$EXP"
  echo "$EXP" | grep -q 'CREATE TABLE' || echo "$EXP" | grep -q 't' || { echo EXPORT_FAIL; exit 1; }
  echo MYSQL_ROUNDTRIP_OK
else
  echo MYSQL_OFFICIAL_NOT_RUNNING
fi

# 脚本不得替面板启动服务
if grep -E 'php-fpm|mysqld|nginx' /proc/self/cmdline 2>/dev/null; then
  true
fi
echo STAGE3_UBUNTU_OK
