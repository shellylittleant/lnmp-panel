package store

import (
	"path/filepath"
	"testing"
)

func TestMigrateAndTemplateIndexes(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.SQL.Exec(`INSERT INTO templates(type,name,origin,content,content_hash,created_at,updated_at)
		VALUES ('nginx_site','PHP 站点','official','A','h1','t','t')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SQL.Exec(`INSERT INTO templates(type,name,origin,content,content_hash,created_at,updated_at)
		VALUES ('nginx_site','PHP 站点','official','B','h2','t','t')`)
	if err == nil {
		t.Fatal("官方同名应拒绝")
	}
	_, err = db.SQL.Exec(`INSERT INTO templates(type,name,origin,content,content_hash,created_at,updated_at)
		VALUES ('nginx_site','我的','user','A','h1','t','t')`)
	if err != nil {
		t.Fatal("用户模板与官方内容相同应允许：", err)
	}
	_, err = db.SQL.Exec(`INSERT INTO templates(type,name,origin,content,content_hash,created_at,updated_at)
		VALUES ('nginx_site','另一个','user','A','h1','t','t')`)
	if err == nil {
		t.Fatal("用户相同哈希应拒绝")
	}
}

func TestJobsTable(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.SQL.Exec(`INSERT INTO jobs(id,kind,phase,progress,detail,started_at) VALUES ('j1','env','queued',0,'','t')`)
	if err != nil {
		t.Fatal(err)
	}
}
