#!/bin/bash
# 阶段 5：日志、监控、定时任务。只做用户会做的动作。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl logrotate cron nginx >/dev/null
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
test "${#CSRF}" -gt 0

SRC=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/logs/sources")
echo "$SRC" | grep -q 'journald' || { echo SOURCES_FAIL; echo "$SRC"; exit 1; }
echo SOURCES_OK

Q=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/logs/query?source=auth&limit=10")
echo "$Q" | grep -q '"ok":true' || { echo QUERY_FAIL; echo "$Q"; cat /tmp/panel.log; exit 1; }
echo QUERY_OK

ST=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/logs/stats?kind=ssh")
echo "$ST" | grep -q '"ok":true' || { echo STATS_FAIL; echo "$ST"; exit 1; }
echo STATS_OK

echo '/var/log/php8.3-fpm.log { daily }' >/etc/logrotate.d/php8.3-fpm
test -f /etc/logrotate.d/nginx
ROT=$(curl -sk -c /tmp/cj -b /tmp/cj -X PUT -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"keep_days":7,"max_size":"50M"}' "$API/logs/rotate")
echo "ROTATE=$ROT"
echo "$ROT" | grep -q '"ok":true' || { echo ROTATE_FAIL; cat /tmp/panel.log; exit 1; }
grep -q 'rotate 7' /etc/logrotate.d/lnmp-panel || { echo ROTATE_FILE_FAIL; cat /etc/logrotate.d/lnmp-panel; exit 1; }
grep -q 'php\*-fpm.pid' /etc/logrotate.d/lnmp-panel || { echo ROTATE_PHP_PID_FAIL; cat /etc/logrotate.d/lnmp-panel; exit 1; }
grep -q mysqladmin /etc/logrotate.d/lnmp-panel || { echo ROTATE_MYSQL_FAIL; cat /etc/logrotate.d/lnmp-panel; exit 1; }
test -f /etc/logrotate.d/nginx.disabled || { echo NGINX_NOT_DISABLED; ls /etc/logrotate.d; exit 1; }
test -f /etc/logrotate.d/php8.3-fpm.disabled || { echo PHP_FPM_NOT_DISABLED; exit 1; }
LRDBG=$(logrotate -d /etc/logrotate.conf 2>&1 || true)
echo "$LRDBG" | grep -q 'duplicate log entry' && { echo DUPLICATE_LOGROTATE; echo "$LRDBG"; exit 1; }
echo ROTATE_OK

# 调度器启动后约 1 秒会采样一次
sleep 2
CUR=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/metrics/current")
echo "$CUR" | grep -q '"ok":true' || { echo METRICS_FAIL; echo "$CUR"; cat /tmp/panel.log; exit 1; }
echo "$CUR" | grep -q '"cpu"' || { echo METRICS_NO_CPU; echo "$CUR"; exit 1; }
echo METRICS_OK

SCH=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/scheduler")
echo "$SCH" | grep -q 'ssl.renew' || { echo SCHED_FAIL; echo "$SCH"; exit 1; }
echo "$SCH" | grep -q 'metrics.sample' || { echo SCHED_SAMPLE_FAIL; echo "$SCH"; exit 1; }
echo "$SCH" | grep -q 'log.aggregate' || { echo SCHED_LOG_FAIL; echo "$SCH"; exit 1; }
echo "$SCH" | grep -q 'running' || { echo SCHED_RUNNING_FAIL; echo "$SCH"; exit 1; }
echo SCHEDULER_OK

CR=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"name":"true","schedule":"*/5 * * * *","command":"/usr/bin/true","enabled":true}' "$API/cron")
echo "CRON=$CR"
echo "$CR" | grep -q '"ok":true' || { echo CRON_FAIL; echo "$CR"; cat /tmp/panel.log; exit 1; }
CID=$(printf '%s' "$CR" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
RUN=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/cron/${CID}/run")
echo "RUN=$RUN"
echo "$RUN" | grep -q '"ok":true' || { echo CRON_RUN_FAIL; echo "$RUN"; exit 1; }
crontab -l | grep -q '/usr/bin/true' || { echo CRONTAB_FAIL; crontab -l; exit 1; }
INJ=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"name":"x\n* * * * * /usr/bin/touch /tmp/pwned","schedule":"* * * * *","command":"/usr/bin/true","enabled":true}' "$API/cron")
echo "INJECT=$INJ"
echo "$INJ" | grep -q '"ok":true' && { echo CRON_INJECT_ACCEPTED; exit 1; }
BAD=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"name":"bad","schedule":"99 * * * *","command":"/usr/bin/true","enabled":true}' "$API/cron")
echo "BADSCHED=$BAD"
echo "$BAD" | grep -q '"ok":true' && { echo CRON_BAD_SCHEDULE_ACCEPTED; exit 1; }
crontab -l | grep -q pwned && { echo CRON_PWNED; crontab -l; exit 1; }
echo CRON_OK

ENV=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/env")
echo "$ENV" | grep -q '"mysql_source":"ubuntu"' || echo "$ENV" | grep -q '"mysql_source":"official"' || {
  echo MYSQL_SOURCE_FAIL; echo "$ENV"; exit 1
}
echo MYSQL_SOURCE_OK

# SSE 跟随：最多 3 秒
set +e
curl -sk -N --max-time 3 -c /tmp/cj -b /tmp/cj "$API/logs/follow?source=auth" >/tmp/sse.txt 2>/tmp/sse.err
set -e
if grep -q 'data:' /tmp/sse.txt || grep -q ': ping' /tmp/sse.txt; then
  echo FOLLOW_OK
else
  echo "FOLLOW 无事件（空日志可接受）"
  cat /tmp/sse.txt || true
  echo FOLLOW_EMPTY_OK
fi

STATUS=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/status")
echo "$STATUS" | grep -q '"metrics"' || { echo STATUS_METRICS_FAIL; echo "$STATUS"; exit 1; }
echo STATUS_OK

wait_job() {
  local jid="$1"
  local st=""
  for i in $(seq 1 90); do
    st=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/jobs/$jid")
    echo "$st" | grep -q '"phase":"done"' && return 0
    echo "$st" | grep -q '"phase":"failed"' && { echo "$st"; return 1; }
    sleep 2
  done
  echo "TIMEOUT $st"; return 1
}

INST=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/env/nginx/install")
echo "NGINX_INSTALL=$INST"
wait_job "$(printf '%s' "$INST" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')" || { echo NGINX_JOB_FAIL; cat /tmp/panel.log; exit 1; }

for key in a b; do
  CR=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
    -H "X-CSRF-Token: $CSRF" \
    -d "{\"site_key\":\"${key}\",\"name\":\"${key}\",\"domains\":[\"${key}.test\"]}" "$API/sites")
  echo "CREATE_$key=$CR"
  echo "$CR" | grep -q '"ok":true' || { echo CREATE_FAIL; echo "$CR"; cat /tmp/panel.log; exit 1; }
done

SITES=$(curl -sk --max-time 2 -c /tmp/cj -b /tmp/cj "$API/sites")
echo "$SITES" | grep -q '"ok":true' || { echo SITES_LIST_FAIL; echo "$SITES"; exit 1; }
echo SITES_OK

kill "$PANEL_PID" 2>/dev/null || true
wait "$PANEL_PID" 2>/dev/null || true
lnmp-panel run --config /etc/lnmp-panel/panel.json >>/tmp/panel.log 2>&1 &
PANEL_PID=$!
ok=0
for i in 1 2 3; do
  code=$(curl -sk --max-time 1 -o /dev/null -w '%{http_code}' -c /tmp/cj2 -b /tmp/cj2 \
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

echo STAGE5_UBUNTU_OK
