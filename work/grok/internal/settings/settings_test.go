package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lnmp-panel/internal/store"
)

func TestApplyRejectsSelfLock(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	err = s.Apply(Update{OpsIPCIDRs: []string{"10.0.0.0/8"}}, "8.8.8.8")
	if !errors.Is(err, ErrSelfLock) {
		t.Fatalf("非回环自锁应拒绝，得到 %v", err)
	}
	err = s.Apply(Update{OpsIPCIDRs: []string{"10.0.0.0/8"}}, "127.0.0.1")
	if err != nil {
		t.Fatalf("回环保存白名单应允许：%v", err)
	}
	ok, err := s.IPAllowed("127.0.0.1")
	if err != nil || !ok {
		t.Fatal("回环应始终可访问")
	}
	ok, err = s.IPAllowed("::1")
	if err != nil || !ok {
		t.Fatal("::1 应始终可访问")
	}
	ok, err = s.IPAllowed("8.8.8.8")
	if err != nil || ok {
		t.Fatal("8.8.8.8 不在白名单内应拒绝")
	}
}

func TestRewriteListenHostOnBindMode(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfgPath := filepath.Join(dir, "panel.json")
	if err := os.WriteFile(cfgPath, []byte(`{"listen":"0.0.0.0:28471","bind_mode":"public","db":"x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	s.SetConfigPath(cfgPath)
	mode := "localhost"
	if err := s.Apply(Update{BindMode: &mode}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["listen"] != "127.0.0.1:28471" {
		t.Fatalf("listen=%v", cfg["listen"])
	}
	if cfg["bind_mode"] != "localhost" {
		t.Fatalf("bind_mode=%v", cfg["bind_mode"])
	}
	if cfg["db"] != "x" {
		t.Fatal("其它字段被丢掉")
	}
}
