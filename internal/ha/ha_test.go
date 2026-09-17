package ha

import (
	"path/filepath"
	"testing"
	"time"

	"lnmp-panel/internal/migrate"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/store"
)

func TestNextInterval(t *testing.T) {
	if got := NextInterval(3 * time.Second); got != time.Minute {
		t.Fatalf("短轮次应保持 1 分钟，got %s", got)
	}
	got := NextInterval(90 * time.Second)
	if got != 100*time.Second {
		t.Fatalf("长轮次应为耗时+余量，got %s", got)
	}
	if SyncAlert(time.Minute) != "" {
		t.Fatal("1 分钟不应告警")
	}
	if SyncAlert(6*time.Minute) == "" {
		t.Fatal("超过 5 分钟应告警")
	}
}

func TestShouldDegrade(t *testing.T) {
	if ShouldDegrade(RoleMaster, 1, false, RoleMaster, 2) {
		t.Fatal("联系不上对端不得降级")
	}
	if ShouldDegrade(RoleMaster, 1, true, RoleStandby, 1) {
		t.Fatal("对端仍是备机不得降级")
	}
	if !ShouldDegrade(RoleMaster, 1, true, RoleMaster, 2) {
		t.Fatal("对端任期更大且已是主机应降级")
	}
	if ShouldDegrade(RoleMaster, 2, true, RoleMaster, 2) {
		t.Fatal("任期相同不降级")
	}
	if ShouldDegrade(RoleStandby, 1, true, RoleMaster, 2) {
		t.Fatal("本机已是备机无需自我保护")
	}
}

func TestHATokenSurvivesMigrateExpire(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := settings.New(db)
	mig := migrate.New(db)
	mig.Settings = st
	mig.Now = func() time.Time { return time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC) }
	mig.IdleFor = time.Hour
	_, err = db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at, purpose) VALUES ('ha-tok','2099-01-01T00:00:00Z','ha')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SQL.Exec(`INSERT INTO pairing_tokens(token_hash, expires_at, purpose) VALUES ('mig-tok','2020-01-01T00:00:00Z','migrate')`)
	if err != nil {
		t.Fatal(err)
	}
	old := mig.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	_, err = db.SQL.Exec(`INSERT INTO peers(address, role, fingerprint, last_seen_at) VALUES ('https://b/x','migrate_source','fp',?)`, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := mig.ExpireStale(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM pairing_tokens WHERE token_hash='ha-tok'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("热备令牌不得被 7 天规则删除：%d %v", n, err)
	}
	if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM pairing_tokens WHERE token_hash='mig-tok'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("迁移令牌应被清理：%d %v", n, err)
	}
}
