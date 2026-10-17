# LNMP 面板

一个面向 LNMP（Linux + Nginx + MySQL + PHP）站点运维的 Linux 可视化控制面板。对标宝塔面板，但只做它功能的一个小子集，拒绝臃肿。

单个 Go 二进制、内嵌 Vue 3 前端、SQLite 存储、systemd 托管。只支持 Ubuntu 24.04 LTS 及以后版本。

## 原则

1. **简单**：功能少而精，界面直接，不做用不到的东西。
2. **专一**：只服务 LNMP 场景，不服务其他技术栈。
3. **避免生态兼容**：不兼容旧版本、不兼容多发行版、不兼容多种同类软件。
4. **扩展靠插件**：核心之外的需求一律以编译期插件实现，核心不膨胀。

## 功能

- 网站管理：建站、Nginx 配置可视化与原文编辑、配置漂移检测、模板机制
- SSL 证书：Let's Encrypt HTTP-01 自动签发与续期，或手动填入证书
- PHP 多版本管理（Ondřej Surý PPA），每站点独立选择版本
- MySQL 管理：建库、建用户、改密码、导入导出（不内嵌 phpMyAdmin）
- 防火墙：基于 nftables 的端口开放与封禁，SSH 端口保护
- 定时任务、日志查看与统计、系统资源监控
- 全目录文件管理（浏览、上传、下载、在线编辑、权限）
- 面板到面板一键迁移：TLS 指纹配对、预检、全量与增量同步
- 容灾热备：主备近实时同步（rsync + MySQL GTID 复制），人工切换与回切
- 安全：随机端口与随机路径、TOTP 二次验证、运维 IP 白名单、登录锁定、CSRF、审计日志

## 支持范围

| 组件 | 要求 |
|---|---|
| 操作系统 | Ubuntu 24.04 LTS 及以后的 LTS 版本，x86_64 或 arm64 |
| Nginx | Ubuntu 源 |
| PHP | 8.2 及以上 |
| MySQL | 8.0 及以上。x86_64 用官方源 8.4 LTS，arm64 用 Ubuntu 源 8.0 |

## 明确不做

多发行版支持（CentOS、Debian 等）、旧版本 PHP/MySQL/Ubuntu、Apache、OpenResty、PostgreSQL、MariaDB、应用商店、WebSSH、邮件服务、FTP、Docker 管理。详细理由见 [docs/09-默认不做功能说明.md](docs/09-默认不做功能说明.md)。

## 安装

全新的 Ubuntu 24.04 服务器，以 root 执行。`OPS_CIDR` 是运维白名单，填你自己电脑的公网 IP，防止试错密码把自己锁在面板外：

```bash
tar -xzf lnmp-panel-amd64.tar.gz -C lnmp && cd lnmp
OPS_CIDR=你的公网IP/32 bash scripts/install.sh
```

安装结束会打印面板地址、用户名 admin 和初始密码。以后随时可以再看：

```bash
cat /etc/lnmp-panel/install.info
```

四件事必须知道：

1. 面板端口随机落在 20000 到 49151，云安全组要放行这一段，以及 22、80、443。
2. 面板地址带一串随机路径，直接访问根路径返回 404，这是有意为之。
3. 面板证书是自签的，浏览器会警告，选"继续访问"。
4. 不在运维白名单内的 IP 连续输错密码 5 次会被锁定。

卸载：

```bash
bash scripts/uninstall.sh
```

## 从源码构建

需要 Go 1.23+ 与 Node.js。

```bash
cd work/grok
./scripts/build.sh      # 构建前端并编译带全部官方插件的二进制到 bin/
go test ./...           # 运行测试
```

改了前端必须重新跑 `./scripts/build.sh`，把生成的 `internal/webui/dist` 与源码一起提交。

## 已知限制

- HTTP-01 不能签通配符证书。
- MySQL 官方源没有 arm64 包，arm64 机器使用 Ubuntu 源的 8.0。
- 热备第一版全部人工操作：无自动接管、无 DNS 联动，备机不对外提供服务。
- 不支持与 Docker 共存，Docker 会改写防火墙规则，与面板独占 nftables 冲突。

## 仓库结构

| 目录 | 内容 |
|---|---|
| `work/grok/` | 面板源码（Go 后端、Vue 前端、安装脚本、systemd 单元、架构文档） |
| `docs/` | 产品原则、设计决策、协作流程、各阶段的执行报告与审核记录 |
| `发布/` | 发布包、操作指南 PDF、版本记录 |

这个项目由需求方、执行 AI 与审核 AI 三方协作完成，整个过程记录在 `docs/` 中，按编号顺序阅读。

## 许可证

[GNU AGPL-3.0](LICENSE)。
