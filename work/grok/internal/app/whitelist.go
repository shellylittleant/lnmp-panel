package app

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/store"
)

func whitelistCmd(args []string) error {
	cfgPath := "/etc/lnmp-panel/panel.json"
	var rest []string
	for i := 0; i < len(args); i++ {
		if (args[i] == "--config" || args[i] == "-config") && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) == 0 {
		return fmt.Errorf("用法：lnmp-panel whitelist show|set <cidr,...>|clear [--config 路径]")
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("读取配置：%w", err)
	}
	db, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	st := settings.New(db)
	switch rest[0] {
	case "show":
		raw, err := st.Get("ops_ip_cidrs")
		if err != nil {
			return err
		}
		if raw == "" || raw == "[]" {
			fmt.Println("当前运维白名单：（空，任意 IP 可访问；本机回环始终放行）")
			return nil
		}
		fmt.Println("当前运维白名单：")
		fmt.Println(raw)
		return nil
	case "clear":
		if err := st.Set("ops_ip_cidrs", "[]"); err != nil {
			return err
		}
		fmt.Println("已清空运维白名单。")
		return nil
	case "set":
		if len(rest) < 2 {
			return fmt.Errorf("用法：lnmp-panel whitelist set <cidr,...>")
		}
		var cidrs []string
		for _, p := range splitComma(strings.Join(rest[1:], ",")) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(p); err != nil {
				if ip := net.ParseIP(p); ip == nil {
					return fmt.Errorf("无效的运维 IP/CIDR：%s", p)
				}
			}
			cidrs = append(cidrs, p)
		}
		b, _ := json.Marshal(cidrs)
		if err := st.Set("ops_ip_cidrs", string(b)); err != nil {
			return err
		}
		fmt.Println("已设置运维白名单：", string(b))
		return nil
	default:
		return fmt.Errorf("未知子命令 %s，用法：lnmp-panel whitelist show|set|clear", rest[0])
	}
}
