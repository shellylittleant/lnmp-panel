# grok 工作区

执行者 **grok** 的独立 git 仓库。

## 阶段 0

架构文档在 `docs/`。审核必须项 M1～M9 已修订，commit 见 git log。

## 阶段 1

面板骨架：Go HTTPS 服务、登录/TOTP/会话/审计、SQLite、Vue 3 壳、安装脚本、systemd。

```bash
# 构建（需 Node 与 Go）
./scripts/build.sh

# 仅测试
go test ./...

# Ubuntu 24.04 上安装（root）
./scripts/install.sh
```

二进制：`bin/lnmp-panel`  
安装后读 `/etc/lnmp-panel/install.info`。

改前端必须跑 `./scripts/build.sh`（或 `cd web && npm run build`）重新生成 `internal/webui/dist` 并与源码一起提交。
