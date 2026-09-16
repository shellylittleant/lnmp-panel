package app

import (
	"path/filepath"
	"testing"

	"lnmp-panel/internal/auth"
	"lnmp-panel/internal/store"
)

func TestBootstrapSkipOS(t *testing.T) {
	dir := t.TempDir()
	res, err := Bootstrap(BootstrapOpts{
		DataDir:  filepath.Join(dir, "data"),
		EtcDir:   filepath.Join(dir, "etc"),
		Listen:   "127.0.0.1:0",
		Password: "GoodPassw0rd!",
		SkipOS:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Username != "admin" || res.Prefix == "" {
		t.Fatalf("%+v", res)
	}
	db, err := store.Open(filepath.Join(dir, "data", "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash string
	if err := db.SQL.QueryRow(`SELECT password_hash FROM admin WHERE id = 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword("GoodPassw0rd!", hash) {
		t.Fatal("密码未写入")
	}
	_, err = Bootstrap(BootstrapOpts{
		DataDir:  filepath.Join(dir, "data"),
		EtcDir:   filepath.Join(dir, "etc"),
		Password: "AnotherPass1!",
		SkipOS:   true,
	})
	if err == nil {
		t.Fatal("重复 bootstrap 应拒绝覆盖管理员")
	}
}
