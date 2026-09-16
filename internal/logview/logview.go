package logview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

var (
	ErrUnknownSource = errors.New("未知日志来源")
	ErrDrift         = errors.New("磁盘配置与面板上次写入不一致，请确认后带 force=true 保存")
	ErrInvalidSite   = errors.New("无效站点")
)

const (
	queryTailBytes = 2 << 20
	statsTailBytes = 256 << 20
)

var siteKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var (
	reRotate = regexp.MustCompile(`(?m)^\s*rotate\s+(\d+)`)
	reSize   = regexp.MustCompile(`(?m)^\s*maxsize\s+(\S+)`)
)

type SiteRef struct {
	ID   int64
	Key  string
	Name string
}

type Service struct {
	db           *store.DB
	tpl          *template.Service
	Sites        func() []SiteRef
	RotatePath   string
	BackupRoot   string
	DistroDir    string // 空则 /etc/logrotate.d
	SkipExec     bool
	Runner       func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
	ExtraSources []Source
	AccessLog    string
	AuthLog      string
	SlowLog      string
	StatsLimit   int64
}

func New(db *store.DB, tpl *template.Service) *Service {
	return &Service{
		db: db, tpl: tpl, Runner: exec.Run,
		RotatePath: "/etc/logrotate.d/lnmp-panel",
		BackupRoot: "/var/lib/lnmp-panel/backups/logrotate",
	}
}

type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"` // file|journal
}

func (s *Service) Sources() []Source {
	out := []Source{
		{ID: "journald", Name: "journald", Path: "journalctl", Kind: "journal"},
		{ID: "auth", Name: "SSH / auth.log", Path: "/var/log/auth.log", Kind: "file"},
		{ID: "syslog", Name: "syslog", Path: "/var/log/syslog", Kind: "file"},
		{ID: "kern", Name: "内核 / 防火墙丢包", Path: "/var/log/kern.log", Kind: "file"},
		{ID: "nginx-access", Name: "Nginx 访问（默认）", Path: "/var/log/nginx/access.log", Kind: "file"},
		{ID: "nginx-error", Name: "Nginx 错误", Path: "/var/log/nginx/error.log", Kind: "file"},
		{ID: "mysql-error", Name: "MySQL 错误", Path: "/var/log/mysql/error.log", Kind: "file"},
		{ID: "mysql-slow", Name: "MySQL 慢查询", Path: "/var/log/mysql/mysql-slow.log", Kind: "file"},
	}
	seen := map[string]bool{}
	for _, x := range out {
		seen[x.ID] = true
	}
	add := func(src Source) {
		if seen[src.ID] {
			return
		}
		seen[src.ID] = true
		out = append(out, src)
	}
	for _, v := range []string{"8.2", "8.3", "8.4", "8.5"} {
		add(Source{ID: "php-fpm-" + v, Name: "PHP-FPM " + v, Path: "/var/log/php" + v + "-fpm.log", Kind: "file"})
	}
	if matches, err := filepath.Glob("/var/log/php*-fpm.log"); err == nil {
		for _, p := range matches {
			base := filepath.Base(p)
			ver := strings.TrimSuffix(strings.TrimPrefix(base, "php"), "-fpm.log")
			add(Source{ID: "php-fpm-" + ver, Name: "PHP-FPM " + ver, Path: p, Kind: "file"})
		}
	}
	if s.Sites != nil {
		for _, st := range s.Sites() {
			add(Source{
				ID: "site-access-" + st.Key, Name: "站点 " + st.Name + " access",
				Path: "/var/log/nginx/" + st.Key + ".access.log", Kind: "file",
			})
			add(Source{
				ID: "site-error-" + st.Key, Name: "站点 " + st.Name + " error",
				Path: "/var/log/nginx/" + st.Key + ".error.log", Kind: "file",
			})
		}
	}
	for _, extra := range s.ExtraSources {
		add(extra)
	}
	return out
}

func (s *Service) sourceByID(id string) (Source, bool) {
	for _, x := range s.Sources() {
		if x.ID == id {
			return x, true
		}
	}
	return Source{}, false
}

type Line struct {
	Text  string `json:"text"`
	Level string `json:"level"`
}

type QueryIn struct {
	Source string
	Site   string
	Q      string
	Level  string
	From   string
	To     string
	Limit  int
}

func (s *Service) ResolveSource(id, site string) (Source, bool) {
	if site != "" && !siteKeyRe.MatchString(site) {
		return Source{}, false
	}
	if id == "" && site != "" {
		id = "site-access-" + site
	}
	if id == "access" && site != "" {
		id = "site-access-" + site
	}
	return s.sourceByID(id)
}

func (s *Service) Query(ctx context.Context, in QueryIn) ([]Line, error) {
	limit := in.Limit
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	if in.Site != "" && !siteKeyRe.MatchString(in.Site) {
		return nil, ErrInvalidSite
	}
	src, ok := s.ResolveSource(in.Source, in.Site)
	if !ok {
		if in.Site != "" && !siteKeyRe.MatchString(in.Site) {
			return nil, ErrInvalidSite
		}
		return nil, ErrUnknownSource
	}
	raw, err := s.readTail(ctx, src, queryTailBytes, in.From, in.To)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(raw, "\n")
	var out []Line
	ql := strings.ToLower(in.Q)
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		ln := strings.TrimRight(lines[i], "\r")
		if ln == "" {
			continue
		}
		if !inRange(ln, in.From, in.To) {
			continue
		}
		lv := GuessLevel(ln)
		if in.Level != "" && lv != in.Level {
			continue
		}
		if ql != "" && !strings.Contains(strings.ToLower(ln), ql) {
			continue
		}
		out = append(out, Line{Text: ln, Level: lv})
	}
	return out, nil
}

func (s *Service) readTail(ctx context.Context, src Source, max int, from, to string) (string, error) {
	if src.Kind == "journal" {
		if s.SkipExec {
			return "", nil
		}
		run := s.Runner
		if run == nil {
			run = exec.Run
		}
		args := []string{"/usr/bin/journalctl", "-n", "500", "--no-pager", "-o", "short-iso"}
		if from != "" {
			args = append(args, "--since", from)
		}
		if to != "" {
			args = append(args, "--until", to)
		}
		r, err := run(ctx, 8*time.Second, args...)
		if err != nil {
			return "", fmt.Errorf("journalctl：%s", exec.Clip(r.Stderr))
		}
		return r.Stdout, nil
	}
	return readTailFile(src.Path, int64(max))
}

func readTailFile(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	sz := st.Size()
	start := sz - max
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	r := bufio.NewReaderSize(f, 64*1024)
	if start > 0 {
		_, _ = r.ReadString('\n')
	}
	var b strings.Builder
	if n := sz - start; n > 0 && n < int64(^uint(0)>>1) {
		b.Grow(int(n))
	}
	if _, err := io.Copy(&b, r); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

func scanFileTail(path string, max int64, fn func(line string)) (truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	sz := st.Size()
	truncated = sz > max
	start := int64(0)
	if truncated {
		start = sz - max
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return truncated, err
	}
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1<<20)
	if start > 0 && sc.Scan() {
		// 丢掉半行
	}
	for sc.Scan() {
		fn(sc.Text())
	}
	return truncated, sc.Err()
}

func (s *Service) Follow(ctx context.Context, sourceID, site string, emit func(Line) error) error {
	src, ok := s.ResolveSource(sourceID, site)
	if !ok {
		return ErrUnknownSource
	}
	var pos int64
	var prev string
	if src.Kind == "file" {
		if st, err := os.Stat(src.Path); err == nil {
			start := st.Size() - 8192
			if start < 0 {
				start = 0
			}
			chunk, newPos, err := TailFile(src.Path, start)
			if err == nil {
				pos = newPos
				for _, ln := range strings.Split(chunk, "\n") {
					ln = strings.TrimRight(ln, "\r")
					if ln == "" {
						continue
					}
					if err := emit(Line{Text: ln, Level: GuessLevel(ln)}); err != nil {
						return err
					}
				}
			}
		}
	} else {
		raw, _ := s.readTail(ctx, src, 64*1024, "", "")
		prev = raw
		lines := strings.Split(raw, "\n")
		start := 0
		if len(lines) > 50 {
			start = len(lines) - 50
		}
		for _, ln := range lines[start:] {
			ln = strings.TrimRight(ln, "\r")
			if ln == "" {
				continue
			}
			if err := emit(Line{Text: ln, Level: GuessLevel(ln)}); err != nil {
				return err
			}
		}
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			if err := emit(Line{Level: "ping"}); err != nil {
				return err
			}
		case <-tick.C:
			if src.Kind == "file" {
				st, err := os.Stat(src.Path)
				if err != nil {
					continue
				}
				if st.Size() < pos {
					pos = 0
				}
				chunk, newPos, err := TailFile(src.Path, pos)
				if err != nil {
					continue
				}
				pos = newPos
				for _, ln := range strings.Split(chunk, "\n") {
					ln = strings.TrimRight(ln, "\r")
					if ln == "" {
						continue
					}
					if err := emit(Line{Text: ln, Level: GuessLevel(ln)}); err != nil {
						return err
					}
				}
				continue
			}
			raw, err := s.readTail(ctx, src, 64*1024, "", "")
			if err != nil {
				continue
			}
			var extra string
			if strings.HasPrefix(raw, prev) {
				extra = raw[len(prev):]
			} else if raw != prev {
				extra = raw
			}
			prev = raw
			for _, ln := range strings.Split(extra, "\n") {
				ln = strings.TrimRight(ln, "\r")
				if ln == "" {
					continue
				}
				if err := emit(Line{Text: ln, Level: GuessLevel(ln)}); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Service) statsLimit() int64 {
	if s.StatsLimit > 0 {
		return s.StatsLimit
	}
	return statsTailBytes
}

func (s *Service) accessPath(siteKey string) (string, error) {
	if siteKey != "" {
		if !siteKeyRe.MatchString(siteKey) {
			return "", ErrInvalidSite
		}
		if s.Sites != nil {
			ok := false
			for _, st := range s.Sites() {
				if st.Key == siteKey {
					ok = true
					break
				}
			}
			if !ok {
				return "", ErrInvalidSite
			}
		}
		return "/var/log/nginx/" + siteKey + ".access.log", nil
	}
	if s.AccessLog != "" {
		return s.AccessLog, nil
	}
	return "/var/log/nginx/access.log", nil
}

func (s *Service) StatsLive(kind, siteKey string) (any, error) {
	switch kind {
	case "ssh":
		path := s.AuthLog
		if path == "" {
			path = "/var/log/auth.log"
		}
		n := 0
		trunc, err := scanFileTail(path, s.statsLimit(), func(ln string) {
			if strings.Contains(ln, "Failed password") || strings.Contains(ln, "authentication failure") {
				n++
			}
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"fails": n, "truncated": trunc}, nil
	case "slowsql":
		path := s.SlowLog
		if path == "" {
			path = "/var/log/mysql/mysql-slow.log"
		}
		var a slowAgg
		trunc, err := scanFileTail(path, s.statsLimit(), a.add)
		if err != nil {
			return nil, err
		}
		return map[string]any{"slow": a.count, "top": a.top, "truncated": trunc}, nil
	default:
		path, err := s.accessPath(siteKey)
		if err != nil {
			return nil, err
		}
		agg := newAccessAgg()
		trunc, err := scanFileTail(path, s.statsLimit(), agg.add)
		if err != nil {
			return nil, err
		}
		st := agg.result()
		return map[string]any{"pv": st.PV, "uv": st.UV, "rt_avg": st.RTAvg, "status": st.Status, "url": st.URL, "ip": st.IP, "referer": st.Referer, "ua": st.UA, "truncated": trunc}, nil
	}
}

func (s *Service) Stats(kind, siteKey, from, to string) (map[string]any, error) {
	if kind == "" {
		kind = "access"
	}
	if siteKey != "" && !siteKeyRe.MatchString(siteKey) {
		return nil, ErrInvalidSite
	}
	live, err := s.StatsLive(kind, siteKey)
	out := map[string]any{"live": live, "kind": kind, "site": siteKey}
	if liveMap, ok := live.(map[string]any); ok {
		if t, ok := liveMap["truncated"].(bool); ok {
			out["truncated"] = t
		}
	}
	if from != "" && to != "" {
		out["hourly"] = s.statsHourly(kind, siteKey, from, to)
	}
	return out, err
}

func (s *Service) statsHourly(kind, siteKey, from, to string) []map[string]any {
	siteID := int64(0)
	if siteKey != "" && s.Sites != nil {
		for _, st := range s.Sites() {
			if st.Key == siteKey {
				siteID = st.ID
				break
			}
		}
	}
	rows, err := s.db.SQL.Query(`SELECT bucket, payload FROM log_stats_hourly WHERE kind=? AND site_id=? AND bucket>=? AND bucket<=? ORDER BY bucket`, kind, siteID, from, to)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var bucket, payload string
		if err := rows.Scan(&bucket, &payload); err != nil {
			continue
		}
		var p any
		_ = json.Unmarshal([]byte(payload), &p)
		out = append(out, map[string]any{"bucket": bucket, "payload": p})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

func inHour(ln string, hour time.Time) bool {
	t := LineTime(ln)
	if t.IsZero() {
		return true
	}
	return !t.Before(hour) && t.Before(hour.Add(time.Hour))
}

func (s *Service) AggregateHour(ctx context.Context) error {
	hour := time.Now().UTC().Truncate(time.Hour)
	now := hour.Format(time.RFC3339)
	lim := s.statsLimit()
	scanAccess := func(path string, siteID int64) {
		agg := newAccessAgg()
		trunc, err := scanFileTail(path, lim, func(ln string) {
			if inHour(ln, hour) {
				agg.add(ln)
			}
		})
		if err != nil {
			return
		}
		st := agg.result()
		payload, _ := json.Marshal(map[string]any{"pv": st.PV, "uv": st.UV, "rt_avg": st.RTAvg, "status": st.Status, "url": st.URL, "ip": st.IP, "referer": st.Referer, "ua": st.UA, "truncated": trunc})
		_, _ = s.db.SQL.Exec(`INSERT INTO log_stats_hourly(bucket, site_id, kind, payload) VALUES (?,?,?,?)
			ON CONFLICT(bucket, site_id, kind) DO UPDATE SET payload=excluded.payload`, now, siteID, "access", string(payload))
	}
	if s.Sites != nil {
		for _, st := range s.Sites() {
			scanAccess("/var/log/nginx/"+st.Key+".access.log", st.ID)
		}
	}
	ap := "/var/log/nginx/access.log"
	if s.AccessLog != "" {
		ap = s.AccessLog
	}
	scanAccess(ap, 0)
	auth := "/var/log/auth.log"
	if s.AuthLog != "" {
		auth = s.AuthLog
	}
	n := 0
	trunc, err := scanFileTail(auth, lim, func(ln string) {
		if inHour(ln, hour) && (strings.Contains(ln, "Failed password") || strings.Contains(ln, "authentication failure")) {
			n++
		}
	})
	if err == nil {
		payload, _ := json.Marshal(map[string]any{"fails": n, "truncated": trunc})
		_, _ = s.db.SQL.Exec(`INSERT INTO log_stats_hourly(bucket, site_id, kind, payload) VALUES (?,?,?,?)
			ON CONFLICT(bucket, site_id, kind) DO UPDATE SET payload=excluded.payload`, now, 0, "ssh", string(payload))
	}
	slow := "/var/log/mysql/mysql-slow.log"
	if s.SlowLog != "" {
		slow = s.SlowLog
	}
	var a slowAgg
	trunc, err = scanFileTail(slow, lim, a.add)
	if err == nil {
		payload, _ := json.Marshal(map[string]any{"slow": a.count, "top": a.top, "truncated": trunc})
		_, _ = s.db.SQL.Exec(`INSERT INTO log_stats_hourly(bucket, site_id, kind, payload) VALUES (?,?,?,?)
			ON CONFLICT(bucket, site_id, kind) DO UPDATE SET payload=excluded.payload`, now, 0, "slowsql", string(payload))
	}
	return nil
}

type RotateForm struct {
	KeepDays int    `json:"keep_days"`
	MaxSize  string `json:"max_size"`
	Content  string `json:"content"`
	Drifted  bool   `json:"drifted"`
	Force    bool   `json:"force"`
}

func (s *Service) setting(key, def string) string {
	var v string
	err := s.db.SQL.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Service) setSetting(key, val string) {
	_, _ = s.db.SQL.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, val)
}

func (s *Service) GetRotate() (RotateForm, error) {
	b, err := os.ReadFile(s.RotatePath)
	if err != nil && !os.IsNotExist(err) {
		return RotateForm{}, err
	}
	f := RotateForm{KeepDays: 14, MaxSize: "100M", Content: string(b)}
	if n := s.setting("log_keep_days", ""); n != "" {
		if v, err := strconv.Atoi(n); err == nil && v > 0 {
			f.KeepDays = v
		}
	}
	if m := reRotate.FindStringSubmatch(string(b)); len(m) == 2 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			f.KeepDays = v
		}
	}
	if m := reSize.FindStringSubmatch(string(b)); len(m) == 2 {
		f.MaxSize = m[1]
	}
	hash := s.setting("logrotate_hash", "")
	if hash != "" && len(b) > 0 {
		f.Drifted = panelFS.HashBytes(b) != hash
	}
	return f, nil
}

func (s *Service) PutRotate(ctx context.Context, in RotateForm) error {
	if in.KeepDays <= 0 {
		in.KeepDays = 14
	}
	if in.MaxSize == "" {
		in.MaxSize = "100M"
	}
	if strings.ContainsAny(in.MaxSize, ";{}\n\r ") {
		return fmt.Errorf("maxsize 含非法字符")
	}
	cur, err := s.GetRotate()
	if err != nil {
		return err
	}
	if cur.Drifted && !in.Force {
		return ErrDrift
	}
	body := in.Content
	if body == "" {
		rec, err := s.tpl.OfficialByName("logrotate", "面板默认")
		if err != nil {
			return err
		}
		body, err = template.Render(rec.Name, rec.Content, map[string]any{"KeepDays": in.KeepDays, "MaxSize": in.MaxSize})
		if err != nil {
			return err
		}
	}
	if err := panelFS.Mkdir(filepath.Dir(s.RotatePath), 0755); err != nil {
		return err
	}
	_, _ = panelFS.Backup(s.RotatePath, s.BackupRoot)
	if err := panelFS.WriteAtomic(s.RotatePath, []byte(body), 0644); err != nil {
		return err
	}
	s.setSetting("logrotate_hash", panelFS.HashBytes([]byte(body)))
	s.setSetting("log_keep_days", strconv.Itoa(in.KeepDays))
	s.disableDistroRotate()
	if s.SkipExec {
		return nil
	}
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	r, err := run(ctx, 10*time.Second, "/usr/sbin/logrotate", "-d", s.RotatePath)
	if err != nil {
		return fmt.Errorf("logrotate -d 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}

func TailFile(path string, after int64) (string, int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", after, err
	}
	sz := st.Size()
	if sz <= after {
		return "", sz, nil
	}
	const followCap = 8 << 20
	if sz-after > followCap {
		after = sz - followCap
	}
	f, err := os.Open(path)
	if err != nil {
		return "", after, err
	}
	defer f.Close()
	if _, err := f.Seek(after, io.SeekStart); err != nil {
		return "", after, err
	}
	b, err := io.ReadAll(bufio.NewReaderSize(f, 64*1024))
	return string(b), sz, err
}

func (s *Service) distroDir() string {
	if s.DistroDir != "" {
		return s.DistroDir
	}
	if s.SkipExec {
		return ""
	}
	return "/etc/logrotate.d"
}

func (s *Service) disableDistroRotate() {
	dir := s.distroDir()
	if dir == "" {
		return
	}
	names := []string{"nginx", "mysql-server", "mysql"}
	for _, n := range names {
		disableRotateFile(filepath.Join(dir, n))
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "php*-fpm"))
	for _, p := range matches {
		if strings.HasSuffix(p, ".disabled") {
			continue
		}
		disableRotateFile(p)
	}
}

func disableRotateFile(src string) {
	if _, err := os.Stat(src); err != nil {
		return
	}
	dst := src + ".disabled"
	if _, err := os.Stat(dst); err == nil {
		return
	}
	_ = os.Rename(src, dst)
}
