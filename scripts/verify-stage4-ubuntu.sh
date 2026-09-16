#!/bin/bash
# umask 077；只做用户会做的动作。不手动 nft/systemctl（除安装 nftables 包）。
# 必须覆盖：应用错误规则后超时回滚，SSH 与面板端口始终可达。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl nftables python3 >/dev/null
install -m 0755 /src/bin/lnmp-panel-linux /usr/local/bin/lnmp-panel
PFX=abcdef0123456789abcdef0123456789
lnmp-panel bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel \
  --listen 127.0.0.1:18443 --prefix "$PFX" --password 'GoodPassw0rd!'
echo BOOTSTRAP_OK
# 占位 SSH：监听 22，验证回滚后端口仍可达
python3 - <<'PY' &
import socket, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 22))
s.listen(5)
time.sleep(300)
PY
sleep 0.3
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

reachable() {
  local port="$1"
  timeout 2 bash -c "echo >/dev/tcp/127.0.0.1/${port}" 2>/dev/null
}

PUT=$(curl -sk -c /tmp/cj -b /tmp/cj -X PUT -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -d '{"ssh_port":22,"panel_port":18443,"input_policy":"drop","public_http":true,"open_ports":[],"allow_cidrs":[]}' \
  "$API/firewall")
echo "PUT=$PUT"
echo "$PUT" | grep -q '"ok":true' || { echo PUT_FAIL; cat /tmp/panel.log; exit 1; }
GET=$(curl -sk -c /tmp/cj -b /tmp/cj "$API/firewall")
echo "$GET" | grep -q 'tcp dport 22' || { echo MISSING_SSH; echo "$GET"; exit 1; }
echo "$GET" | grep -q 'tcp dport 18443' || { echo MISSING_PANEL; exit 1; }

APP=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/firewall/apply")
echo "APPLY=$APP"
echo "$APP" | grep -q '"ok":true' || { echo APPLY_FAIL; cat /tmp/panel.log; nft list ruleset; exit 1; }
CNF=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/firewall/confirm")
echo "CONFIRM=$CNF"
echo "$CNF" | grep -q '"ok":true' || { echo CONFIRM_FAIL; exit 1; }
reachable 22 || { echo SSH_AFTER_GOOD_FAIL; exit 1; }
curl -sk --max-time 3 "https://127.0.0.1:18443/${PFX}/" >/dev/null || { echo PANEL_AFTER_GOOD_FAIL; exit 1; }
echo GOOD_RULES_OK

# 锁死新连接（保留 established，让本次 apply 的 HTTP 响应能返回），不确认，等超时回滚
BAD='flush ruleset
table inet lnmp_panel {
  chain input {
    type filter hook input priority 0; policy drop;
    ct state established,related accept
  }
  chain forward { type filter hook forward priority 0; policy drop; }
  chain output { type filter hook output priority 0; policy accept; }
}
'
RAW=$(curl -sk -c /tmp/cj -b /tmp/cj -X PUT -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  --data-binary "{\"content\":$(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' <<<"$BAD"),\"force\":true}" \
  "$API/firewall/raw")
echo "RAW=$RAW"
echo "$RAW" | grep -q '"ok":true' || { echo RAW_FAIL; exit 1; }
APP2=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/firewall/apply")
echo "APPLY_BAD=$APP2"
echo "$APP2" | grep -q '"ok":true' || { echo APPLY_BAD_FAIL; cat /tmp/panel.log; exit 1; }
if curl -sk --max-time 3 "https://127.0.0.1:18443/${PFX}/" >/dev/null 2>&1; then
  echo "LOCKOUT_NOT_SEEN 新连接仍通，继续等回滚"
else
  echo LOCKOUT_OK
fi
# 不 confirm。等待默认 60 秒窗口结束
echo WAIT_ROLLBACK
sleep 65
# 面板与 SSH 必须恢复
curl -sk --max-time 5 "https://127.0.0.1:18443/${PFX}/" >/dev/null || { echo PANEL_AFTER_ROLLBACK_FAIL; nft list ruleset; cat /tmp/panel.log; exit 1; }
reachable 22 || { echo SSH_AFTER_ROLLBACK_FAIL; nft list ruleset; exit 1; }
echo ROLLBACK_OK

# G4/S1：含显式 drop 的坏规则，超时回滚必须替换而不是叠加
DROP='flush ruleset
table inet lnmp_panel {
  chain input {
    type filter hook input priority 0; policy drop;
    ct state established,related accept
    tcp dport 22 drop
    tcp dport 18443 drop
  }
  chain forward { type filter hook forward priority 0; policy drop; }
  chain output { type filter hook output priority 0; policy accept; }
}
'
RAW2=$(curl -sk -c /tmp/cj -b /tmp/cj -X PUT -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  --data-binary "{\"content\":$(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' <<<"$DROP"),\"force\":true}" \
  "$API/firewall/raw")
echo "RAW_DROP=$RAW2"
echo "$RAW2" | grep -q '"ok":true' || { echo RAW_DROP_FAIL; exit 1; }
APP3=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/firewall/apply")
echo "APPLY_DROP=$APP3"
echo "$APP3" | grep -q '"ok":true' || { echo APPLY_DROP_FAIL; cat /tmp/panel.log; exit 1; }
echo WAIT_DROP_ROLLBACK
sleep 65
curl -sk --max-time 5 "https://127.0.0.1:18443/${PFX}/" >/dev/null || { echo PANEL_AFTER_DROP_ROLLBACK_FAIL; nft list ruleset; cat /tmp/panel.log; exit 1; }
reachable 22 || { echo SSH_AFTER_DROP_ROLLBACK_FAIL; nft list ruleset; exit 1; }
echo EXPLICIT_DROP_ROLLBACK_OK

# G4/S2：窗口内杀进程重启后自动回滚
RAW3=$(curl -sk -c /tmp/cj -b /tmp/cj -X PUT -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  --data-binary "{\"content\":$(python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' <<<"$DROP"),\"force\":true}" \
  "$API/firewall/raw")
echo "RAW_KILL=$RAW3"
echo "$RAW3" | grep -q '"ok":true' || { echo RAW_KILL_FAIL; exit 1; }
APP4=$(curl -sk -c /tmp/cj -b /tmp/cj -X POST -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" -d '{}' "$API/firewall/apply")
echo "APPLY_KILL=$APP4"
echo "$APP4" | grep -q '"ok":true' || { echo APPLY_KILL_FAIL; cat /tmp/panel.log; exit 1; }
kill "$PANEL_PID" || true
sleep 0.5
umask 077
lnmp-panel run --config /etc/lnmp-panel/panel.json >/tmp/panel2.log 2>&1 &
PANEL_PID=$!
ok=0
for i in $(seq 1 15); do
  if reachable 22; then ok=1; break; fi
  sleep 0.2
done
test "$ok" = "1" || { echo SSH_AFTER_RESTART_FAIL; nft list ruleset; cat /tmp/panel2.log; exit 1; }
curl -sk --max-time 5 "https://127.0.0.1:18443/${PFX}/" >/dev/null || { echo PANEL_AFTER_RESTART_FAIL; nft list ruleset; cat /tmp/panel2.log; exit 1; }
echo RESTART_RECOVER_OK
echo STAGE4_UBUNTU_OK
