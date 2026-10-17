package cron

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/store"
)

func TestValidateAndCRUD(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	s.SkipExec = true
	if _, err := s.Create(context.Background(), "x", "bad", "/usr/bin/true", true); err == nil {
		t.Fatal("坏调度")
	}
	if _, err := s.Create(context.Background(), "x", "* * * * *", "echo hi", true); err == nil {
		t.Fatal("相对路径")
	}
	j, err := s.Create(context.Background(), "ping", "*/5 * * * *", "/usr/bin/true", true)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := s.List()
	if len(items) != 1 || items[0].ID != j.ID {
		t.Fatalf("%+v", items)
	}
	if err := s.Delete(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background(), "sh", "* * * * *", "/bin/bash -c echo", true); err == nil {
		t.Fatal("应拒绝 shell")
	}
	j2, err := s.Create(context.Background(), "ping", "*/5 * * * *", "/usr/bin/true", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow(context.Background(), j2.ID); err != nil {
		t.Fatal(err)
	}
	items, _ = s.List()
	if items[0].LastStatus != "ok" {
		t.Fatalf("%+v", items[0])
	}
}

func TestNameInjectAndBadSchedule(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	s.SkipExec = true
	if _, err := s.Create(context.Background(), "x\n* * * * * /usr/bin/touch /tmp/pwned", "* * * * *", "/usr/bin/true", true); err == nil {
		t.Fatal("名称换行应拒绝")
	}
	if _, err := s.Create(context.Background(), "x#inject", "* * * * *", "/usr/bin/true", true); err == nil {
		t.Fatal("名称含 # 应拒绝")
	}
	if _, err := s.Create(context.Background(), "ok", "99 * * * *", "/usr/bin/true", true); err == nil {
		t.Fatal("分钟 99 应拒绝")
	}
	if _, err := s.Create(context.Background(), "ok", "* * * * 8", "/usr/bin/true", true); err == nil {
		t.Fatal("星期 8 应拒绝")
	}
	items, _ := s.List()
	if len(items) != 0 {
		t.Fatalf("拒绝后不应留行：%+v", items)
	}
}

func TestCreateRollbackWhenCrontabFails(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	s.SkipExec = false
	s.Runner = func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error) {
		return exec.Result{}, nil
	}
	s.IORunner = func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error) {
		return exec.Result{Stderr: "boom"}, fmt.Errorf("fail")
	}
	if _, err := s.Create(context.Background(), "ping", "*/5 * * * *", "/usr/bin/true", true); err == nil {
		t.Fatal("应失败")
	}
	items, _ := s.List()
	if len(items) != 0 {
		t.Fatalf("写 crontab 失败不应留孤儿行：%+v", items)
	}
}

func TestCrontabEscapePercent(t *testing.T) {
	if crontabEscape(`/usr/bin/date +%Y`) != `/usr/bin/date +\%Y` {
		t.Fatal(crontabEscape(`/usr/bin/date +%Y`))
	}
}

func TestCrontabReadFailureDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	wrote := false
	s.Runner = func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error) {
		return exec.Result{Stderr: "permission denied"}, fmt.Errorf("exit 1")
	}
	s.IORunner = func(ctx context.Context, d time.Duration, stdin io.Reader, argv ...string) (exec.Result, error) {
		wrote = true
		return exec.Result{}, nil
	}
	if _, err := s.Create(context.Background(), "ping", "*/5 * * * *", "/usr/bin/true", true); err == nil {
		t.Fatal("读取失败应拒绝写入")
	}
	if wrote {
		t.Fatal("读取失败不应调用 crontab -")
	}
	s.Runner = func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error) {
		return exec.Result{Stderr: "no crontab for root"}, fmt.Errorf("exit 1")
	}
	if _, err := s.Create(context.Background(), "ping", "*/5 * * * *", "/usr/bin/true", true); err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("no crontab 应继续写入")
	}
}

func TestStripPanelBlock(t *testing.T) {
	in := "MAILTO=root\n" + beginMark + "\n* * * * * /usr/bin/true\n" + endMark + "\n# keep\n"
	out := stripPanelBlock(in)
	if strings.Contains(out, "/usr/bin/true") || !strings.Contains(out, "MAILTO") || !strings.Contains(out, "# keep") {
		t.Fatal(out)
	}
}
