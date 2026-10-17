#!/bin/bash
# LNMP 面板安装脚本。仅支持 Ubuntu 24.04 LTS 及以上 LTS。可重复执行（升级二进制）。
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "错误：请用 root 运行。" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_SRC="${ROOT}/bin/lnmp-panel"
if [[ ! -x "${BIN_SRC}" ]]; then
  BIN_SRC="$(command -v lnmp-panel || true)"
fi
if [[ -z "${BIN_SRC}" || ! -x "${BIN_SRC}" ]]; then
  echo "错误：找不到 lnmp-panel 二进制。请先执行 scripts/build.sh。" >&2
  exit 1
fi

if command -v ufw >/dev/null 2>&1; then
  ufw --force disable || true
  if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now ufw >/dev/null 2>&1 || true
  fi
fi

if [[ ! -x /usr/sbin/nft ]]; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y nftables
fi

install -d -m 0755 /usr/local/bin
install -m 0755 "${BIN_SRC}" /usr/local/bin/lnmp-panel
install -d -m 0700 /etc/lnmp-panel /var/lib/lnmp-panel /var/log/lnmp-panel
install -m 0644 "${ROOT}/systemd/lnmp-panel.service" /etc/systemd/system/lnmp-panel.service

if [[ -f /etc/lnmp-panel/panel.json ]]; then
  echo "检测到已有配置，跳过初始化，仅更新二进制与服务。"
else
  ARGS=(bootstrap --data-dir /var/lib/lnmp-panel --etc-dir /etc/lnmp-panel)
  if [[ -n "${OPS_CIDR:-}" ]]; then
    ARGS+=(--ops-cidr "${OPS_CIDR}")
  fi
  if [[ "${BIND_MODE:-}" == "localhost" ]]; then
    ARGS+=(--bind-mode localhost)
  fi
  /usr/local/bin/lnmp-panel "${ARGS[@]}"
fi

if [[ ! -f /etc/lnmp-panel/nftables.conf ]]; then
  cat >/etc/lnmp-panel/nftables.conf <<'NFT'
# 阶段 1 占位。阶段 4 写入完整规则集。此处不加 hook，避免安装时改写系统默认策略。
table inet lnmp_panel {
}
NFT
  chmod 0600 /etc/lnmp-panel/nftables.conf
fi
echo 'include "/etc/lnmp-panel/nftables.conf"' >/etc/nftables.conf

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
  systemctl enable nftables.service >/dev/null 2>&1 || true
  systemctl enable lnmp-panel.service
  systemctl restart lnmp-panel.service
  echo "已启动 lnmp-panel.service"
else
  echo "未检测到 systemd，请手动运行：lnmp-panel run --config /etc/lnmp-panel/panel.json"
fi

if [[ -f /etc/lnmp-panel/install.info ]]; then
  echo "----- 安装信息 -----"
  cat /etc/lnmp-panel/install.info
fi
