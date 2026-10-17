package jobs

import (
	"path/filepath"
	"testing"

	"lnmp-panel/internal/store"
)

func TestAbandonUnfinished(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	id, err := s.Start("install")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AbandonUnfinished(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "failed" || got.Detail != "面板重启，结果未知" || got.FinishedAt == nil {
		t.Fatalf("%+v", got)
	}
	if _, err := s.Start("install"); err != nil {
		t.Fatal(err)
	}
}
