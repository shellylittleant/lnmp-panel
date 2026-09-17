package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestBusyTimeoutIs15s(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.SQL.QueryRow(`PRAGMA busy_timeout`).Scan(&n); err != nil || n != 15000 {
		t.Fatalf("busy_timeout=%d %v", n, err)
	}
}

func TestSQLiteDSNHasPragmas(t *testing.T) {
	d := sqliteDSN("/tmp/x.db")
	if !strings.Contains(d, "_pragma=busy_timeout(15000)") || !strings.Contains(d, "_pragma=journal_mode(WAL)") {
		t.Fatalf("DSN %s", d)
	}
}

func TestBusyTimeoutOnPooledConns(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conns := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		c, err := db.SQL.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i, c := range conns {
		var n int
		var mode string
		if err := c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&n); err != nil || n != 15000 {
			t.Fatalf("conn %d busy_timeout=%d %v", i, n, err)
		}
		if err := c.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
			t.Fatalf("conn %d journal_mode=%s %v", i, mode, err)
		}
	}
}

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

func TestStage3Tables(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO php_versions(version, installed) VALUES ('8.3', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO mysql_databases(name, charset, "collate", created_at) VALUES ('blog','utf8mb4','utf8mb4_0900_ai_ci','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO mysql_users(user, host, created_at) VALUES ('u','localhost','t')`); err != nil {
		t.Fatal(err)
	}
}

func TestFirewallStateRow(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM firewall_state WHERE id=1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("firewall_state %d %v", n, err)
	}
}

func TestStage5Tables(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO cron_jobs(name,schedule,command,enabled) VALUES ('x','* * * * *','/usr/bin/true',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO metrics_raw(at,cpu,mem_used,mem_total,disk_used,disk_total,net_in,net_out,load1) VALUES ('t',0,0,0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO metrics_hourly(bucket,cpu_avg,mem_avg,disk_avg,net_in_sum,net_out_sum,load_avg) VALUES ('t',0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO log_stats_hourly(bucket,site_id,kind,payload) VALUES ('t',0,'access','{}')`); err != nil {
		t.Fatal(err)
	}
}

func TestStage6Tables(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at) VALUES ('h','t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint) VALUES ('https://b/x','migrate_target','fp')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO migrate_jobs(peer_id, mode, phase, detail, started_at) VALUES (1,'full','handshake','','t')`); err != nil {
		t.Fatal(err)
	}
}

func TestStage7HAState(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	var term int
	if err := db.SQL.QueryRow(`SELECT role, term FROM ha_state WHERE id=1`).Scan(&role, &term); err != nil {
		t.Fatal(err)
	}
	if role != "standalone" || term != 0 {
		t.Fatalf("ha_state %s %d", role, term)
	}
	if _, err := db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at, purpose) VALUES ('h','t','ha')`); err != nil {
		t.Fatal(err)
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
