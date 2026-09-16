package cron

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"path/filepath"

	"lnmp-panel/internal/exec"
	"lnmp-panel/internal/store"
)

const (
	beginMark = "# LNMP-PANEL-BEGIN"
	endMark   = "# LNMP-PANEL-END"
)

var (
	ErrNotFound = fmt.Errorf("定时任务不存在")
	schedRe     = regexp.MustCompile(`^([A-Za-z0-9*/,#-]+ ){4}[A-Za-z0-9*/,#-]+$`)
)

type Job struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Schedule   string  `json:"schedule"`
	Command    string  `json:"command"`
	Enabled    bool    `json:"enabled"`
	LastRunAt  *string `json:"last_run_at"`
	LastStatus string  `json:"last_status"`
}

type Service struct {
	db       *store.DB
	SkipExec bool
	Runner   func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)
}

func New(db *store.DB) *Service {
	return &Service{db: db, Runner: exec.Run}
}

func (s *Service) List() ([]Job, error) {
	rows, err := s.db.SQL.Query(`SELECT id,name,schedule,command,enabled,last_run_at,last_status FROM cron_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var last sql.NullString
		var en int
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Command, &en, &last, &j.LastStatus); err != nil {
			return nil, err
		}
		j.Enabled = en == 1
		if last.Valid {
			j.LastRunAt = &last.String
		}
		out = append(out, j)
	}
	if out == nil {
		out = []Job{}
	}
	return out, rows.Err()
}

func validate(name, schedule, command string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("名称不能为空")
	}
	if !schedRe.MatchString(strings.TrimSpace(schedule)) {
		return fmt.Errorf("调度须为标准五段 cron")
	}
	if strings.ContainsAny(command, "\n\r") {
		return fmt.Errorf("命令不能含换行")
	}
	cmd := strings.TrimSpace(command)
	if !strings.HasPrefix(cmd, "/") {
		return fmt.Errorf("命令必须是绝对路径")
	}
	argv := strings.Fields(cmd)
	if len(argv) == 0 {
		return fmt.Errorf("命令不能为空")
	}
	base := filepath.Base(argv[0])
	if _, bad := cronDenied[base]; bad {
		return fmt.Errorf("禁止执行：%s", argv[0])
	}
	return nil
}

var cronDenied = map[string]struct{}{
	"sh": {}, "bash": {}, "dash": {}, "zsh": {},
	"docker": {}, "ufw": {}, "iptables": {}, "pecl": {}, "apache2": {},
}

func (s *Service) Create(ctx context.Context, name, schedule, command string, enabled bool) (Job, error) {
	if err := validate(name, schedule, command); err != nil {
		return Job{}, err
	}
	en := 0
	if enabled {
		en = 1
	}
	res, err := s.db.SQL.Exec(`INSERT INTO cron_jobs(name,schedule,command,enabled,last_status) VALUES (?,?,?,?, '')`, name, schedule, command, en)
	if err != nil {
		return Job{}, err
	}
	id, _ := res.LastInsertId()
	if err := s.syncCrontab(ctx); err != nil {
		return Job{}, err
	}
	items, err := s.List()
	if err != nil {
		return Job{}, err
	}
	for _, j := range items {
		if j.ID == id {
			return j, nil
		}
	}
	return Job{ID: id, Name: name, Schedule: schedule, Command: command, Enabled: enabled}, nil
}

func (s *Service) Update(ctx context.Context, id int64, name, schedule, command string, enabled bool) error {
	if err := validate(name, schedule, command); err != nil {
		return err
	}
	en := 0
	if enabled {
		en = 1
	}
	res, err := s.db.SQL.Exec(`UPDATE cron_jobs SET name=?, schedule=?, command=?, enabled=? WHERE id=?`, name, schedule, command, en, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return s.syncCrontab(ctx)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	res, err := s.db.SQL.Exec(`DELETE FROM cron_jobs WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return s.syncCrontab(ctx)
}

func (s *Service) RunNow(ctx context.Context, id int64) error {
	var cmd string
	err := s.db.SQL.QueryRow(`SELECT command FROM cron_jobs WHERE id=?`, id).Scan(&cmd)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if s.SkipExec {
		_, _ = s.db.SQL.Exec(`UPDATE cron_jobs SET last_run_at=?, last_status=? WHERE id=?`, now, "ok", id)
		return nil
	}
	argv := strings.Fields(cmd)
	r, err := exec.RunAbs(ctx, 60*time.Second, argv...)
	st := "ok"
	if err != nil {
		st = exec.Clip(err.Error() + " " + r.Stderr)
	}
	_, _ = s.db.SQL.Exec(`UPDATE cron_jobs SET last_run_at=?, last_status=? WHERE id=?`, now, st, id)
	return err
}

func (s *Service) Drifted(ctx context.Context) bool {
	if s.SkipExec {
		return false
	}
	items, err := s.List()
	if err != nil {
		return false
	}
	block := extractPanelBlock(s.readCrontab(ctx))
	var want strings.Builder
	for _, j := range items {
		if !j.Enabled {
			continue
		}
		fmt.Fprintf(&want, "# id=%d name=%s\n%s %s\n", j.ID, j.Name, j.Schedule, j.Command)
	}
	return strings.TrimSpace(block) != strings.TrimSpace(want.String())
}

func (s *Service) syncCrontab(ctx context.Context) error {
	if s.SkipExec {
		return nil
	}
	items, err := s.List()
	if err != nil {
		return err
	}
	existing := s.readCrontab(ctx)
	outside := stripPanelBlock(existing)
	var b strings.Builder
	b.WriteString(outside)
	if !strings.HasSuffix(outside, "\n") && outside != "" {
		b.WriteByte('\n')
	}
	b.WriteString(beginMark + "\n")
	for _, j := range items {
		if !j.Enabled {
			continue
		}
		fmt.Fprintf(&b, "# id=%d name=%s\n%s %s\n", j.ID, j.Name, j.Schedule, j.Command)
	}
	b.WriteString(endMark + "\n")
	r, err := exec.RunIO(ctx, 8*time.Second, strings.NewReader(b.String()), "/usr/bin/crontab", "-")
	if err != nil {
		return fmt.Errorf("写入 crontab 失败：%s", exec.Clip(r.Stderr))
	}
	return nil
}

func (s *Service) readCrontab(ctx context.Context) string {
	run := s.Runner
	if run == nil {
		run = exec.Run
	}
	r, err := run(ctx, 5*time.Second, "/usr/bin/crontab", "-l")
	if err != nil {
		return ""
	}
	return r.Stdout
}

func stripPanelBlock(s string) string {
	var out []string
	in := false
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		ln := sc.Text()
		if strings.TrimSpace(ln) == beginMark {
			in = true
			continue
		}
		if strings.TrimSpace(ln) == endMark {
			in = false
			continue
		}
		if !in {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}

func extractPanelBlock(s string) string {
	var out []string
	in := false
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		ln := sc.Text()
		if strings.TrimSpace(ln) == beginMark {
			in = true
			continue
		}
		if strings.TrimSpace(ln) == endMark {
			in = false
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
