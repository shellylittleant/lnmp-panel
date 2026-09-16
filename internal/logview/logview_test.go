package logview

import (
	"context"
	"os"
	"path/filepath"
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
	if !containsAll(string(b), "rotate 7", "maxsize 50M") {
		t.Fatalf("%s", b)
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
