package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lnmp-panel/internal/store"
)

func TestSampleAndHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	s.ProcStat = filepath.Join(dir, "stat")
	s.MemInfo = filepath.Join(dir, "mem")
	s.LoadAvg = filepath.Join(dir, "load")
	s.NetDev = filepath.Join(dir, "net")
	s.Root = dir
	_ = os.WriteFile(s.ProcStat, []byte("cpu  100 0 50 850 0 0 0 0\n"), 0644)
	_ = os.WriteFile(s.MemInfo, []byte("MemTotal:        1000000 kB\nMemAvailable:     400000 kB\n"), 0644)
	_ = os.WriteFile(s.LoadAvg, []byte("0.50 0.40 0.30 1/100 1\n"), 0644)
	_ = os.WriteFile(s.NetDev, []byte("Inter-|   Receive\nface |bytes\neth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\nlo: 9 0 0 0 0 0 0 0 9 0 0 0 0 0 0 0\n"), 0644)
	if err := s.Sample(); err != nil {
		t.Fatal(err)
	}
	sm, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if sm.MemTotal != 1000000*1024 || sm.Load1 != 0.5 {
		t.Fatalf("%+v", sm)
	}
	if sm.NetIn != 1000 || sm.NetOut != 2000 {
		t.Fatalf("net %+v", sm)
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	from := hour.Add(-time.Hour)
	_, _ = s.db.SQL.Exec(`INSERT OR REPLACE INTO metrics_raw(at,cpu,mem_used,mem_total,disk_used,disk_total,net_in,net_out,load1,extra)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, from.Add(time.Minute).Format(time.RFC3339), 10.0, 100, 1000, 1, 2, 10, 20, 0.2, "{}")
	_, _ = s.db.SQL.Exec(`INSERT OR REPLACE INTO metrics_raw(at,cpu,mem_used,mem_total,disk_used,disk_total,net_in,net_out,load1,extra)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, from.Add(30*time.Minute).Format(time.RFC3339), 30.0, 300, 1000, 1, 2, 40, 80, 0.4, "{}")
	if err := s.Rollup(); err != nil {
		t.Fatal(err)
	}
	hist, err := s.History(from.Format(time.RFC3339), hour.Format(time.RFC3339), "hour")
	if err != nil || len(hist) != 1 {
		t.Fatalf("%+v %v", hist, err)
	}
	if hist[0].CPU != 20 {
		t.Fatalf("cpu avg %+v", hist[0])
	}
}
