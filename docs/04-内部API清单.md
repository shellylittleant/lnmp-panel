# 04 内部 API 清单

内部 API 的真相是 **Go 服务接口**。HTTP 是给 Vue 和未来主控面板用的适配器。热备按钮、迁移、插件都只调服务接口，不直接碰 `exec`/`store`。

公共前缀：`https://<host>:<port>/<path_prefix>/api/v1`。

未带此前缀的请求：404，无 WWW-Authenticate，无 Server 指纹。

## 1. 通用约定

### 1.1 鉴权

| 类 | 规则 |
|---|---|
| 公开 | 仅 `POST /auth/login`（仍受 IP 锁定与失败计数） |
| 已登录 | 其余全部。Cookie 会话，绑定 IP |
| CSRF | 除 GET/HEAD 外需要请求头 `X-CSRF-Token`，值来自 `GET /auth/session` |
| 插件路由 | 同样走会话，不得自建登录 |

### 1.2 写操作审计

凡非 GET 的成功或失败（除登录密码字段外）记 `audit_logs`。`action` 为 `METHOD path`（例如 `PUT /api/v1/settings`），由 HTTP 中间件自动生成，前端按此筛选。`request_id` 一律由服务端生成，忽略客户端传入的 `X-Request-Id`。

### 1.3 信封

成功：

```json
{"ok": true, "data": {}}
```

失败：

```json
{"ok": false, "error": {"code": "conflict", "message": "站点域名已存在"}}
```

`message` 必须是中文。`code` 稳定英文，供前端分支。

| code | HTTP | 含义 |
|---|---|---|
| `unauthorized` | 401 | 未登录或会话失效 |
| `forbidden` | 403 | IP 不在白名单、TOTP 未通过 |
| `locked` | 423 | 登录锁定 |
| `not_found` | 404 | 资源不存在（面板前缀错误也是裸 404，不走信封） |
| `invalid` | 400 | 参数错误 |
| `conflict` | 409 | 域名冲突、漂移未确认覆盖等 |
| `precondition` | 412 | 预检失败、确认窗口过期 |
| `not_supported` | 400 | 版本低于下限、检测到 MariaDB/Docker |
| `internal` | 500 | 未预期 |

列表类：`data.items` + `data.total`。不分页的小列表可只给 `items`。

### 1.4 Go 接口形态

每个领域一个 `Service`。HTTP handler 只依赖接口。示例（阶段 1 实现时按此切分，允许加方法，不允许让 handler 绕过）：

```go
type SiteService interface {
    List(ctx Context) ([]Site, error)
    Get(ctx Context, id int64) (SiteDetail, error)
    Create(ctx Context, in SiteIn) (Site, error)
    Update(ctx Context, id int64, in SiteIn) (Site, error)
    Delete(ctx Context, id int64) error
    Enable(ctx Context, id int64, on bool) error
    GetRaw(ctx Context, id int64) (RawFile, error)
    PutRaw(ctx Context, id int64, body string) error
}

type HAService interface {
    Status(ctx Context) (HAStatus, error)
    Pair(ctx Context, in PairIn) error
    Promote(ctx Context) error          // 启用主模式
    Demote(ctx Context, peer string) error
    StartSync(ctx Context) error
    SyncStatus(ctx Context) (SyncStatus, error)
    Health(ctx Context) (Health, error)
    PeerRole(ctx Context) (RoleView, error) // 供对端自我保护查询
}
```

完整接口在阶段落地时与下表 HTTP 一一对应。本文不把每个方法的 Go 签名再抄一遍，避免两份清单漂移：**下表即清单**。

## 2. 鉴权与面板（阶段 1）

| 方法 | 路径 | 审计 | 说明 |
|---|---|---|---|
| POST | `/auth/login` | 是（不含密码） | 用户名密码；若已开 TOTP 返回 `need_totp=true`，再调 verify |
| POST | `/auth/totp/verify` | 是 | 登录第二步 |
| POST | `/auth/logout` | 是 | 作废当前会话 |
| GET | `/auth/session` | 否 | 当前用户、过期时间、CSRF、是否已开 TOTP |
| DELETE | `/auth/sessions/:id` | 是 | 踢会话；`:id` 亦可为 `current` |
| GET | `/auth/sessions` | 否 | 会话列表 |
| POST | `/auth/password` | 是 | 改密，需旧密码 |
| POST | `/auth/totp/setup` | 是 | 返回 otpauth URI 与密钥，尚未启用。已开启 TOTP 时返回 409，须先用密码关闭再绑定 |
| POST | `/auth/totp/enable` | 是 | 校验一个码后启用 |
| POST | `/auth/totp/disable` | 是 | 需密码 |
| GET | `/settings` | 否 | 面板设置 |
| PUT | `/settings` | 是 | 白名单、bind_mode。新白名单非空时必须包含调用方 IP，否则 400。改 bind_mode 同步改写 panel.json 的 listen 主机 |
| GET | `/status` | 否 | 进程、OS、磁盘、服务是否在跑（阶段 1 给 OS/进程；服务项阶段 3/5 填） |
| GET | `/audit` | 否 | 查询参数 `from,to,action,ip` |
| POST | `/panel/restart` | 是 | 重启自身（改端口后） |
| GET | `/jobs/:id` | 否 | 长任务进度。阶段 1 建表即可，真实任务从阶段 3 起 |

## 3. 模板（阶段 2 起，表阶段 1 可先建）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/templates?type=` | 按类型列官方+用户 |
| GET | `/templates/:id` | 含原文 |
| POST | `/templates` | 手动新建用户模板 |
| PUT | `/templates/:id` | 官方模板不可改内容（只读）；用户模板可原文编辑 |
| DELETE | `/templates/:id` | 官方不可删；被站点引用则 409 |
| POST | `/templates/:id/export` | 返回 JSON 文件 |
| POST | `/templates/import` | 导入为用户模板 |

保存站点/防火墙等若产生新哈希，服务层自动 `templates` 插入，不强制前端先调 POST。

## 4. 站点 / Nginx / SSL（阶段 2）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/env/nginx/install` | 从 Ubuntu 源安装 Nginx。长时间任务，返回 `{job_id}`，进度 `GET /jobs/:id` |
| GET | `/sites` | 列表 |
| POST | `/sites` | 建站：域名、根目录、PHP 版本、模板选择 |
| GET | `/sites/:id` | 详情 + `drifted` + 所用模板 |
| PUT | `/sites/:id` | 表单保存；若 drifted 需 `force=true` |
| DELETE | `/sites/:id` | 删 conf、停站点；默认不删 `/var/www` 文件，查询参数 `purge_files=true` 才删 |
| POST | `/sites/:id/enable` | 启用（写 sites-enabled） |
| POST | `/sites/:id/disable` | 停用 |
| GET | `/sites/:id/nginx` | 磁盘上的 conf 原文 |
| PUT | `/sites/:id/nginx` | 原文保存并 `nginx -t` + reload，进入 raw_mode |
| POST | `/sites/:id/nginx/use-template` | 退出 raw_mode，用模板重写 |
| GET/PUT | `/sites/:id/redirects` | 301 列表 |
| POST | `/sites/:id/ssl/issue` | HTTP-01 申请 |
| POST | `/sites/:id/ssl/renew` | 手动续期 |
| GET | `/certs` | 证书与到期 |

## 5. 环境 / PHP / MySQL（阶段 3）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/env` | Nginx/PHP/MySQL 是否已装、版本 |
| POST | `/env/php/repo` | 添加 Ondřej PHP PPA。长时间任务，返回 `{job_id}` |
| POST | `/env/mysql/install` | 写入 MySQL 官方源并安装 mysql-community-server。长时间任务，返回 `{job_id}`。失败不回退 Ubuntu 源 |
| GET | `/php/versions` | 已装与可装（≥8.2） |
| POST | `/php/versions/:ver/install` | apt 安装该版本 fpm/cli |
| GET | `/php/versions/:ver/extensions` | 已装扩展 |
| POST | `/php/versions/:ver/extensions` | body `{name}`，只接受 apt 包名映射表内的扩展 |
| DELETE | `/php/versions/:ver/extensions/:name` | 卸载扩展 |
| GET | `/php/versions/:ver/ini` | 表单字段 + 原文 + drifted |
| PUT | `/php/versions/:ver/ini` | 表单保存 |
| PUT | `/php/versions/:ver/ini/raw` | 原文保存 |
| POST | `/php/versions/:ver/ini/use-template` | 退出原文模式，按官方「生产默认」重写片段 |
| GET/PUT | `/php/versions/:ver/pool` | 同 ini |
| PUT | `/php/versions/:ver/pool/raw` | pool 原文 |
| POST | `/php/versions/:ver/pool/use-template` | 退出原文模式 |
| GET | `/mysql/databases` | |
| POST | `/mysql/databases` | `{name, charset}` |
| DELETE | `/mysql/databases/:name` | |
| GET | `/mysql/users` | 不含密码 |
| POST | `/mysql/users` | `{user, host, password, databases}` |
| PUT | `/mysql/users/:id/password` | |
| DELETE | `/mysql/users/:id` | |
| POST | `/mysql/databases/:name/export` | 返回 SQL 流或生成文件再下载 |
| POST | `/mysql/databases/:name/import` | multipart SQL |
| GET/PUT | `/mysql/cnf` | 面板托管片段表单 |
| GET/PUT | `/mysql/cnf/raw` | 原文 |
| POST | `/mysql/cnf/use-template` | 退出原文模式 |

长时间 apt/导入：只走 `GET /jobs/:id`，禁止再开 `/env/jobs/:id` 之类第二入口。

## 6. 防火墙（阶段 4）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/firewall` | 表单视图（SSH 端口、面板端口、开放端口列表、IP 集合、策略）+ drifted |
| PUT | `/firewall` | 保存但不应用 |
| GET | `/firewall/raw` | 当前将要加载的规则集文本 |
| PUT | `/firewall/raw` | 原文保存，raw_mode |
| POST | `/firewall/apply` | `nft -c` 通过后加载，进入确认窗口（默认 60 秒），窗口内旧规则可回滚 |
| POST | `/firewall/confirm` | 取消回滚定时器 |
| POST | `/firewall/rollback` | 立即回滚 |
| POST | `/firewall/ban` | 内部/登录模块也可调服务方法；HTTP 给界面手动封禁 |

确认窗口期间面板自己的心跳若中断，到期回滚。这是自我保护的实现，不是自动运维。

## 7. 日志、监控、定时任务（阶段 5）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/logs/sources` | 来源清单（journald、auth、nginx error、各站点 access/error、php、mysql、nft 丢包） |
| GET | `/logs/query` | `source,site,from,to,q,level,limit` |
| GET | `/logs/follow` | SSE，实时跟随 |
| GET | `/logs/stats` | `site,from,to,kind` → PV/UV/状态码/URL/IP/Referer/UA 或 SSH 失败或慢 SQL |
| GET/PUT | `/logs/rotate` | logrotate 配置 |
| GET | `/metrics/current` | 瞬时 |
| GET | `/metrics/history` | `from,to,step=raw\|hour` |
| GET | `/cron` | 面板托管的 crontab 项 |
| POST | `/cron` | |
| PUT | `/cron/:id` | |
| DELETE | `/cron/:id` | |
| POST | `/cron/:id/run` | 立刻执行 |
| GET | `/scheduler` | 内部 Job 列表与上次运行（只读） |

## 8. 迁移（阶段 6）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/migrate/token` | B 机生成一次性令牌，短 TTL。明文为 `<证书SHA-256指纹前16位hex>.<随机串>` |
| POST | `/migrate/connect` | A 机：`{address, token}`。先按令牌首段校验 B 的 TLS 指纹，通过后才发令牌；成功后把完整指纹写入 `peers.fingerprint` 并固定 |
| POST | `/migrate/preflight` | B 执行：OS、磁盘、端口、PHP/MySQL 可装 |
| POST | `/migrate/start` | `{mode: full\|incr}` |
| GET | `/migrate/status` | 进度 |
| POST | `/migrate/cancel` | 作废钥、停 rsync |

同步内容按 docs/04 清单：站点文件、Nginx、PHP、MySQL、SSL、防火墙、面板元数据与模板。第一版不做定时备份 API。

## 9. 热备（阶段 7）

全部人工触发，无「自动接管」端点。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/ha/status` | 角色、term、间隔、落后、复制是否正常 |
| POST | `/ha/pair` | 复用配对令牌机制 |
| POST | `/ha/promote` | **启用主模式**：停收同步、MySQL 可写、启用站点 |
| POST | `/ha/demote` | 降级为备：404 模式、只读、白名单防火墙 |
| POST | `/ha/sync/start` | **备机**开始从主机拉：rsync、配置、确认复制在跑。主机调用返回错误 |
| GET | `/ha/sync/status` | 落后时间、上一轮扫描耗时、复制状态 |
| GET | `/ha/health` | 本机健康，供对端查询 |
| GET | `/ha/peer-role` | 返回本机角色与 term；**供对端启动自我保护**。此接口仍需双方配对后的机器身份，不能对公网匿名打开 |

`/ha/peer-role` 与 `/ha/health` 使用配对时交换的机器令牌（不是管理员 Cookie），避免备机故障时运维还要先登录 A 才能让 A 问 B。令牌只绑定对端指纹。管理员界面按钮仍走 Cookie 会话。

第一版不提供 DNS 修改 API。

## 10. 插件（阶段 8）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/plugins` | 已编译插件、是否启用 |
| PUT | `/plugins/:id` | `{enabled}` 运行时启停 |

插件自有 HTTP 挂在 `/api/v1/plugin/<id>/...`，由注册表在启用时挂上、停用时卸下。

## 11. 未来主控预留

- 不在第一版实现主控。
- 主控只应使用上述 HTTP + 管理员会话或以后加的「机器令牌」。
- 不单独再做一套 gRPC。
- 不在领域包里写「仅界面能做」的旁路。
