# 如何增加一个官方插件

只接受进本仓库的 Go 源码，随 `go build -tags plugin_all` 发布。不加载 `.so`，不跑外部进程。

1. 在 `plugins/<id>/` 新建包，文件头：

```go
//go:build plugin_<id> || plugin_all

package yourplugin
```

2. 实现 `plugin.Plugin`（见 `internal/plugin`），在 `init` 里 `plugin.Register(&P{})`。
3. 在 `plugins/register_<id>.go` 增加同样的 build tag，并 `import _ "lnmp-panel/plugins/<id>"`。
4. HTTP 只挂在 `/api/v1/plugin/<id>/...`，页面用 `Pages()` 声明式 schema，由核心渲染。不要自己监听端口。
5. 只通过 `plugin.Context` 拿 WWW/设置/插件 KV，不要 import `database/sql`，不要绕过 `internal/exec` 白名单。
6. 路径拼接必须走 `plugin.Confine`，禁止 `..`，禁止碰到 `/etc/lnmp-panel` 与 `/var/lib/lnmp-panel`。
7. 运行时启停写 `plugins` 表；`enabled=0` 时不调用 `OnStart`、不挂路由与页面。

审查重点：`exec` 使用、路径拼接、是否试图改 `admin` / 删除 `audit_logs`。
