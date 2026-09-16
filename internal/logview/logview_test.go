package logview

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

func TestParseAccessAndStats(t *testing.T) {
	line := `1.2.3.4 - - [16/Sep/2026:12:00:00 +0000] "GET /hello HTTP/1.1" 200 123 "-" "ua"`
	h, ok := ParseAccessLine(line)
	if !ok || h.IP != "1.2.3.4" || h.URL != "/hello" || h.Status != 200 {
		t.Fatalf("%+v %v", h, ok)
	}
	st := AggregateAccess([]string{line, line, `5.5.5.5 - - [16/Sep/2026:12:00:01 +0000] "GET /x HTTP/1.1" 404 1 "-" "b"`})
	if st.PV != 3 || st.UV != 2 || st.Status["200"] != 2 {
		t.Fatalf("%+v", st)
	}
}

func TestSSHFailCount(t *testing.T) {
	n := CountSSHFails([]string{
		"Failed password for root from 1.1.1.1",
		"Accepted password",
		"authentication failure; logname=",
	})
	if n != 2 {
		t.Fatal(n)
	}
}

func TestSlowSQLAndLineTime(t *testing.T) {
	lines := []string{
		"# Query_time: 3.2 Lock_time: 0",
		"SELECT * FROM t",
		"# Query_time: 1.1 Lock_time: 0",
		"SELECT 1",
	}
	top := TopSlowSQL(lines, 10)
	if len(top) != 2 || top[0].Seconds != 3.2 {
		t.Fatalf("%+v", top)
	}
	line := `1.2.3.4 - - [16/Sep/2026:12:00:00 +0000] "GET /hello HTTP/1.1" 200 123 "-" "ua" 0.05`
	h, ok := ParseAccessLine(line)
	if !ok || h.RT != 0.05 {
		t.Fatalf("%+v %v", h, ok)
	}
	if LineTime(line).IsZero() {
		t.Fatal("应解析到时间")
	}
}

func TestQueryUnknownAndTail(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, template.New(db))
	s.SkipExec = true
	_, err = s.Query(context.Background(), QueryIn{Source: "nope"})
	if err != ErrUnknownSource {
		t.Fatalf("got %v", err)
	}
	p := filepath.Join(dir, "a.log")
	if err := os.WriteFile(p, []byte("hello\nworld\n"), 0644); err != nil {
		t.Fatal(err)
	}
	txt, pos, err := TailFile(p, 0)
	if err != nil || txt != "hello\nworld\n" {
		t.Fatalf("%q %d %v", txt, pos, err)
	}
	more, _, err := TailFile(p, pos)
	if err != nil || more != "" {
		t.Fatalf("more %q", more)
	}
}

func TestRotateRender(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	s := New(db, tpl)
	s.SkipExec = true
	s.RotatePath = filepath.Join(dir, "lnmp-panel")
	if err := s.PutRotate(context.Background(), RotateForm{KeepDays: 7, MaxSize: "50M"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.RotatePath)
	if !containsAll(string(b), "rotate 7", "maxsize 50M", "php*-fpm.pid", "mysqladmin") {
		t.Fatalf("%s", b)
	}
}

func TestDisableDistroRotate(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tpl := template.New(db)
	if err := tpl.SyncOfficial(); err != nil {
		t.Fatal(err)
	}
	distro := filepath.Join(dir, "logrotate.d")
	if err := os.MkdirAll(distro, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(distro, "nginx"), []byte("/var/log/nginx/*.log {}\n"), 0644)
	_ = os.WriteFile(filepath.Join(distro, "php8.3-fpm"), []byte("/var/log/php8.3-fpm.log {}\n"), 0644)
	s := New(db, tpl)
	s.SkipExec = true
	s.RotatePath = filepath.Join(dir, "lnmp-panel")
	s.DistroDir = distro
	if err := s.PutRotate(context.Background(), RotateForm{KeepDays: 7, MaxSize: "50M"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(distro, "nginx")); err == nil {
		t.Fatal("nginx 应被改名为 .disabled")
	}
	if _, err := os.Stat(filepath.Join(distro, "nginx.disabled")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(distro, "php8.3-fpm.disabled")); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidSite(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, template.New(db))
	s.SkipExec = true
	_, err = s.Query(context.Background(), QueryIn{Site: "../../etc"})
	if err != ErrInvalidSite {
		t.Fatalf("got %v", err)
	}
	_, err = s.Stats("access", "../../root", "", "")
	if err != ErrInvalidSite {
		t.Fatalf("stats %v", err)
	}
}

func rssBytes() (int64, bool) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "VmRSS:") {
			fs := strings.Fields(ln)
			if len(fs) < 2 {
				return 0, false
			}
			n, err := strconv.ParseInt(fs[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return n * 1024, true
		}
	}
	return 0, false
}

func writeAccessLog(t *testing.T, path string, size int, last string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(`1.2.3.4 - - [16/Sep/2026:12:00:00 +0000] "GET /hello HTTP/1.1" 200 123 "-" "ua"` + "\n")
	var n int
	for n < size {
		w, err := f.Write(line)
		if err != nil {
			t.Fatal(err)
		}
		n += w
	}
	if last != "" {
		_, _ = f.WriteString(last + "\n")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestQueryAndStatsDoNotLoadWholeFile(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, template.New(db))
	s.SkipExec = true
	p := filepath.Join(dir, "access.log")
	last := `9.9.9.9 - - [16/Sep/2026:12:00:00 +0000] "GET /last HTTP/1.1" 200 1 "-" "z"`
	writeAccessLog(t, p, 50<<20, last)
	s.ExtraSources = []Source{{ID: "big", Name: "big", Path: p, Kind: "file"}}
	s.AccessLog = p

	raw, err := readTailFile(p, queryTailBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > queryTailBytes+4096 {
		t.Fatalf("readTail 读了 %d 字节", len(raw))
	}

	runtime.GC()
	before, hasRSS := rssBytes()
	items, err := s.Query(context.Background(), QueryIn{Source: "big", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := rssBytes()
	if len(items) == 0 || !strings.Contains(items[0].Text, "/last") {
		t.Fatalf("应读到末尾行：%+v", items)
	}
	if hasRSS {
		delta := after - before
		if delta > 20<<20 {
			t.Fatalf("查询后 RSS 增加 %d 字节，上限 20MB", delta)
		}
	}

	runtime.GC()
	before, hasRSS = rssBytes()
	live, err := s.StatsLive("access", "")
	if err != nil {
		t.Fatal(err)
	}
	after, _ = rssBytes()
	m, _ := live.(map[string]any)
	if m["pv"] == nil || m["pv"].(int) < 1 {
		t.Fatalf("stats %+v", live)
	}
	if hasRSS {
		delta := after - before
		if delta > 20<<20 {
			t.Fatalf("统计后 RSS 增加 %d 字节，上限 20MB", delta)
		}
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || stringIndex(s, sub) >= 0)
}
func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
