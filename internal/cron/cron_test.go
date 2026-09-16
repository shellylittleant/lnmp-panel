package cron

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

func TestStripPanelBlock(t *testing.T) {
	in := "MAILTO=root\n" + beginMark + "\n* * * * * /usr/bin/true\n" + endMark + "\n# keep\n"
	out := stripPanelBlock(in)
	if strings.Contains(out, "/usr/bin/true") || !strings.Contains(out, "MAILTO") || !strings.Contains(out, "# keep") {
		t.Fatal(out)
	}
}
