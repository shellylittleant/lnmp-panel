#!/bin/bash
# 探测 Ondřej PPA 与 MySQL 官方 apt 源，不装面板。失败不得回退 Ubuntu 源。
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg software-properties-common >/dev/null

echo "===== Ondřej PPA ====="
add-apt-repository -y ppa:ondrej/php
apt-get update -qq
for v in 8.2 8.3 8.4 8.5 8.6; do
  if apt-cache show "php${v}-fpm" >/dev/null 2>&1; then
    ver=$(apt-cache policy "php${v}-fpm" | awk '/Candidate:/ {print $2}')
    echo "PHP_OK php${v}-fpm candidate=$ver"
  else
    echo "PHP_MISS php${v}-fpm"
  fi
done

echo "===== MySQL official ====="
install -d -m 0755 /usr/share/keyrings
curl -fsSL https://repo.mysql.com/RPM-GPG-KEY-mysql-2025 -o /usr/share/keyrings/mysql.asc
chmod 0644 /usr/share/keyrings/mysql.asc
. /etc/os-release
codename=${VERSION_CODENAME:-noble}
echo "CODENAME=$codename"
echo "deb [signed-by=/usr/share/keyrings/mysql.asc] http://repo.mysql.com/apt/ubuntu ${codename} mysql-8.4-lts mysql-tools" \
  >/etc/apt/sources.list.d/mysql.list
if ! apt-get update -qq; then
  echo "MYSQL_UPDATE_FAIL"
  ls -l /etc/apt/sources.list.d/mysql.list
  cat /etc/apt/sources.list.d/mysql.list
  exit 1
fi
for p in mysql-community-server mysql-server mysql-community-client; do
  if apt-cache show "$p" >/dev/null 2>&1; then
    ver=$(apt-cache policy "$p" | awk '/Candidate:/ {print $2}')
    echo "MYSQL_OK $p candidate=$ver"
  else
    echo "MYSQL_MISS $p"
  fi
done
apt-cache search mysql-community | head -40
echo PROBE_DONE
