package app

import (
	"path/filepath"
	"strings"
	"testing"

	"lnmp-panel/internal/store"
)

func TestWhitelistCLI(t *testing.T) {
	dir := t.TempDir()
	res, err := Bootstrap(BootstrapOpts{
		DataDir:  filepath.Join(dir, "data"),
		EtcDir:   filepath.Join(dir, "etc"),
		Listen:   "127.0.0.1:18443",
		Password: "GoodPassw0rd!",
		SkipOS:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CLI([]string{"whitelist", "set", "10.0.0.0/8,192.168.1.1", "--config", res.Config}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "data", "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v string
	if err := db.SQL.QueryRow(`SELECT value FROM settings WHERE key='ops_ip_cidrs'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v, "10.0.0.0/8") || !strings.Contains(v, "192.168.1.1") {
		t.Fatalf("%s", v)
	}
	if err := CLI([]string{"whitelist", "--config", res.Config, "clear"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL.QueryRow(`SELECT value FROM settings WHERE key='ops_ip_cidrs'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "[]" {
		t.Fatalf("clear 后 %s", v)
	}
	if err := CLI([]string{"whitelist", "set", "not-an-ip", "--config", res.Config}); err == nil {
		t.Fatal("无效 CIDR 应拒绝")
	}
}
