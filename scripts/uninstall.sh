#!/bin/bash
set -euo pipefail
if [[ ${EUID} -ne 0 ]]; then
  echo "错误：请用 root 运行。" >&2
  exit 1
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now lnmp-panel.service >/dev/null 2>&1 || true
fi
rm -f /usr/local/bin/lnmp-panel /etc/systemd/system/lnmp-panel.service
rm -rf /etc/lnmp-panel /var/lib/lnmp-panel /var/log/lnmp-panel
if [[ -d /etc/logrotate.d ]]; then
  for f in /etc/logrotate.d/*.disabled; do
    [[ -e "$f" ]] || continue
    orig="${f%.disabled}"
    if [[ ! -e "$orig" ]]; then
      mv "$f" "$orig"
    fi
  done
fi
echo "已卸载面板。站点目录 /var/www 未删除。"
