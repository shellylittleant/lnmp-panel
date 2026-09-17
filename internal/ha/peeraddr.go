package ha

import (
	"net"
	"strings"

	"lnmp-panel/internal/migrate"
)

// rewriteURLHost 把 https://主机:端口/前缀 的主机换成配对请求来源 IP，端口与前缀不变。
func rewriteURLHost(addr, ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return strings.TrimSpace(addr)
	}
	hostPort, prefix, err := migrate.SplitHostPortFromAddr(addr)
	if err != nil {
		return strings.TrimSpace(addr)
	}
	_, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return strings.TrimSpace(addr)
	}
	return "https://" + net.JoinHostPort(ip, port) + "/" + prefix
}
