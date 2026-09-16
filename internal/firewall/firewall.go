package firewall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lnmp-panel/internal/exec"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/settings"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

var (
	ErrDrift   = errors.New("磁盘配置与面板上次写入不一致，请确认后带 force=true 保存")
	ErrRawMode = errors.New("处于原文模式，请先恢复为模板渲染")
	ErrPending = errors.New("已有未确认的规则变更，请先确认或回滚")
	ErrNoDraft = errors.New("没有待应用的规则，请先保存")
	ErrPort    = errors.New("端口无效")
	ErrCIDR    = errors.New("无效的 IP/CIDR")
	ErrPolicy  = errors.New("策略只能是 accept 或 drop")
)

const (
	Canonical   = "/etc/lnmp-panel/nftables.conf"
	SystemNFT   = "/etc/nftables.conf"
	IncludeLine = "include \"/etc/lnmp-panel/nftables.conf\"\n"
)

type Runner func(ctx context.Context, d time.Duration, argv ...string) (exec.Result, error)

type Form struct {
	SSHPort     int      `json:"ssh_port"`
	PanelPort   int      `json:"panel_port"`
	OpenPorts   []int    `json:"open_ports"`
	UDPPorts    []int    `json:"udp_ports"`
	AllowCIDRs  []string `json:"allow_cidrs"`
	InputPolicy string   `json:"input_policy"`
	PublicHTTP  bool     `json:"public_http"`
	Force       bool     `json:"force"`
}

type Vars struct {
	SSHPort     int
	PanelPort   int
	OpenPorts   []int
	UDPPorts    []int
	AllowCIDRs  []string
	InputPolicy string
	PublicHTTP  bool
}

type View struct {
	Form             Form    `json:"form"`
	Content          string  `json:"content"`
	Drifted          bool    `json:"drifted"`
	RawMode          bool    `json:"raw_mode"`
	Pending          bool    `json:"pending"`
	ConfirmDeadline  *string `json:"confirm_deadline"`
	LastAppliedAt    string  `json:"last_applied_at"`
	SSHProtected     bool    `json:"ssh_protected"`
	PanelProtected   bool    `json:"panel_protected"`
}

type Service struct {
	db         *store.DB
	tpl        *template.Service
	Settings   *settings.Service
	Runner     Runner
	Canonical  string
	Draft      string
	SystemNFT  string
	BackupRoot string
	PanelPort  int
	Window     time.Duration
	SkipNFT    bool
	mu         sync.Mutex
	cancel     context.CancelFunc
}

func New(db *store.DB, tpl *template.Service, st *settings.Service) *Service {
	return &Service{
		db:         db,
		tpl:        tpl,
		Settings:   st,
		Runner:     exec.Run,
		Canonical:  Canonical,
		Draft:      "/var/lib/lnmp-panel/nftables.draft",
		SystemNFT:  SystemNFT,
		BackupRoot: "/var/lib/lnmp-panel/backups/nftables",
		Window:     60 * time.Second,
	}
}

func (s *Service) run() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return exec.Run
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func (f *Form) normalize(panelPort int) {
	if f.SSHPort == 0 {
		f.SSHPort = 22
	}
	if f.PanelPort == 0 {
		f.PanelPort = panelPort
	}
	if f.InputPolicy == "" {
		f.InputPolicy = "drop"
	}
	if f.OpenPorts == nil {
		f.OpenPorts = []int{}
	}
	if f.UDPPorts == nil {
		f.UDPPorts = []int{}
	}
	if f.AllowCIDRs == nil {
		f.AllowCIDRs = []string{}
	}
}

func (f Form) validate() error {
	if !validPort(f.SSHPort) || !validPort(f.PanelPort) {
		return ErrPort
	}
	if f.InputPolicy != "accept" && f.InputPolicy != "drop" {
		return ErrPolicy
	}
	for _, p := range append(append([]int{}, f.OpenPorts...), f.UDPPorts...) {
		if !validPort(p) {
			return ErrPort
		}
	}
	for _, c := range f.AllowCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			return ErrCIDR
		}
		if strings.ContainsAny(c, ";{}\n\r ") {
			return ErrCIDR
		}
		if _, _, err := net.ParseCIDR(c); err != nil {
			if net.ParseIP(c) == nil {
				return fmt.Errorf("%w：%s", ErrCIDR, c)
			}
		}
	}
	return nil
}

func (s *Service) panelPort() int {
	if s.PanelPort > 0 {
		return s.PanelPort
	}
	if s.Settings != nil {
		p, _ := s.Settings.Get("listen_port")
		if n, err := strconv.Atoi(p); err == nil && validPort(n) {
			return n
		}
	}
	return 0
}

func (s *Service) Ensure() error {
	if err := panelFS.Mkdir(filepath.Dir(s.Canonical), 0700); err != nil {
		return err
	}
	if err := panelFS.Mkdir(filepath.Dir(s.Draft), 0700); err != nil {
		return err
	}
	if err := panelFS.Mkdir(s.BackupRoot, 0700); err != nil {
		return err
	}
	if _, err := os.Stat(s.SystemNFT); err == nil || os.IsNotExist(err) {
		_ = panelFS.WriteAtomic(s.SystemNFT, []byte(IncludeLine), 0644)
	}
	if _, err := os.Stat(s.Canonical); err != nil {
		f := Form{}
		f.normalize(s.panelPort())
		body, err := s.render(f)
		if err != nil {
			return err
		}
		if err := panelFS.WriteAtomic(s.Canonical, []byte(body), 0600); err != nil {
			return err
		}
	}
	s.disableUFW()
	return nil
}

func (s *Service) disableUFW() {
	if s.SkipNFT {
		return
	}
	run := s.run()
	ctx := context.Background()
	_, _ = run(ctx, 10*time.Second, "/usr/bin/systemctl", "disable", "--now", "ufw")
	_, _ = run(ctx, 10*time.Second, "/usr/bin/systemctl", "enable", "nftables")
}

func (s *Service) render(f Form) (string, error) {
	name := "主机默认"
	if s.Settings != nil {
		bm, _ := s.Settings.Get("bind_mode")
		if bm == "localhost" {
			name = "仅本机面板"
		}
	}
	rec, err := s.tpl.OfficialByName("nftables", name)
	if err != nil {
		return "", fmt.Errorf("缺少官方 nftables 模板")
	}
	v := Vars{
		SSHPort: f.SSHPort, PanelPort: f.PanelPort, OpenPorts: f.OpenPorts, UDPPorts: f.UDPPorts,
		AllowCIDRs: f.AllowCIDRs, InputPolicy: f.InputPolicy, PublicHTTP: f.PublicHTTP,
	}
	return template.Render(rec.Name, rec.Content, v)
}

func (s *Service) state() (raw bool, hash, pending, deadline, applied string) {
	var rawN int
	var h, p, d, a sql.NullString
	_ = s.db.SQL.QueryRow(`SELECT raw_mode, last_written_hash, pending_backup, confirm_deadline, last_applied_at FROM firewall_state WHERE id=1`).
		Scan(&rawN, &h, &p, &d, &a)
	return rawN == 1, h.String, p.String, d.String, a.String
}

func (s *Service) draftBody() string {
	if b, err := os.ReadFile(s.Draft); err == nil {
		return string(b)
	}
	if b, err := os.ReadFile(s.Canonical); err == nil {
		return string(b)
	}
	return ""
}

func (s *Service) Get() (View, error) {
	if err := s.Ensure(); err != nil {
		return View{}, err
	}
	body := s.draftBody()
	raw, hash, pending, deadline, applied := s.state()
	h := panelFS.HashBytes([]byte(body))
	drifted := hash != "" && h != hash
	f := s.loadForm()
	if raw {
		f = parseForm(body, s.panelPort())
	}
	v := View{
		Form: f, Content: body, Drifted: drifted, RawMode: raw,
		Pending: pending != "" && deadline != "", LastAppliedAt: applied,
		SSHProtected: strings.Contains(body, fmt.Sprintf("tcp dport %d", f.SSHPort)),
		PanelProtected: strings.Contains(body, fmt.Sprintf("tcp dport %d", f.PanelPort)),
	}
	if v.Pending && deadline != "" {
		v.ConfirmDeadline = &deadline
	}
	return v, nil
}

func parseForm(body string, panel int) Form {
	f := Form{SSHPort: 22, PanelPort: panel, InputPolicy: "drop", PublicHTTP: true, OpenPorts: []int{}, UDPPorts: []int{}, AllowCIDRs: []string{}}
	if strings.Contains(body, "policy accept") {
		f.InputPolicy = "accept"
	}
	if strings.Contains(body, "tcp dport { 80, 443 }") {
		f.PublicHTTP = true
	}
	return f
}

func (s *Service) Put(ctx context.Context, in Form) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Ensure(); err != nil {
		return err
	}
	raw, hash, pending, _, _ := s.state()
	if pending != "" {
		return ErrPending
	}
	if raw {
		return ErrRawMode
	}
	body := s.draftBody()
	h := panelFS.HashBytes([]byte(body))
	if hash != "" && h != hash && !in.Force {
		return ErrDrift
	}
	in.normalize(s.panelPort())
	if err := in.validate(); err != nil {
		return err
	}
	out, err := s.render(in)
	if err != nil {
		return err
	}
	s.saveForm(in)
	return s.writeDraft(out, false)
}

func (s *Service) PutRaw(ctx context.Context, content string, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Ensure(); err != nil {
		return err
	}
	_, hash, pending, _, _ := s.state()
	if pending != "" {
		return ErrPending
	}
	body := s.draftBody()
	h := panelFS.HashBytes([]byte(body))
	if hash != "" && h != hash && !force {
		return ErrDrift
	}
	if _, err := s.tpl.EnsureUser("nftables", content, nil); err != nil {
		return err
	}
	return s.writeDraft(content, true)
}

func (s *Service) UseTemplate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := Form{}
	f.normalize(s.panelPort())
	out, err := s.render(f)
	if err != nil {
		return err
	}
	s.saveForm(f)
	return s.writeDraft(out, false)
}

func (s *Service) saveForm(f Form) {
	if s.Settings == nil {
		return
	}
	b, _ := json.Marshal(f)
	_ = s.Settings.Set("firewall_form", string(b))
}

func (s *Service) loadForm() Form {
	f := Form{}
	f.normalize(s.panelPort())
	if s.Settings == nil {
		return f
	}
	v, _ := s.Settings.Get("firewall_form")
	if v != "" {
		_ = json.Unmarshal([]byte(v), &f)
		f.normalize(s.panelPort())
	}
	return f
}

func (s *Service) writeDraft(body string, raw bool) error {
	if err := panelFS.WriteAtomic(s.Draft, []byte(body), 0600); err != nil {
		return err
	}
	rawN := 0
	if raw {
		rawN = 1
	}
	_, err := s.db.SQL.Exec(`UPDATE firewall_state SET last_written_hash=?, raw_mode=? WHERE id=1`, panelFS.HashBytes([]byte(body)), rawN)
	return err
}

func (s *Service) Apply(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Ensure(); err != nil {
		return err
	}
	_, _, pending, _, _ := s.state()
	if pending != "" {
		return ErrPending
	}
	draft := s.Draft
	if _, err := os.Stat(draft); err != nil {
		return ErrNoDraft
	}
	if err := s.checkFile(ctx, draft); err != nil {
		return err
	}
	bakDir := filepath.Join(s.BackupRoot, time.Now().UTC().Format("20060102T150405Z"))
	if err := panelFS.Mkdir(bakDir, 0700); err != nil {
		return err
	}
	bak := filepath.Join(bakDir, "live.nft")
	if err := s.dumpLive(ctx, bak); err != nil {
		return err
	}
	if err := s.loadFile(ctx, draft); err != nil {
		_ = s.loadFile(ctx, bak)
		return err
	}
	deadline := time.Now().UTC().Add(s.Window).Format(time.RFC3339)
	_, err := s.db.SQL.Exec(`UPDATE firewall_state SET pending_backup=?, confirm_deadline=? WHERE id=1`, bak, deadline)
	if err != nil {
		return err
	}
	s.startTimer()
	return nil
}

func (s *Service) startTimer() {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	w := s.Window
	go func() {
		t := time.NewTimer(w)
		defer t.Stop()
		select {
		case <-t.C:
			_ = s.Rollback(context.Background())
		case <-ctx.Done():
		}
	}()
}

func (s *Service) Confirm(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, pending, deadline, _ := s.state()
	if pending == "" || deadline == "" {
		return fmt.Errorf("没有待确认的规则")
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	body := s.draftBody()
	if err := panelFS.WriteAtomic(s.Canonical, []byte(body), 0600); err != nil {
		return err
	}
	now := s.db.Now()
	_, err := s.db.SQL.Exec(`UPDATE firewall_state SET pending_backup=NULL, confirm_deadline=NULL, last_applied_at=? WHERE id=1`, now)
	return err
}

func (s *Service) Rollback(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, pending, deadline, _ := s.state()
	if pending == "" && deadline == "" {
		return fmt.Errorf("没有可回滚的规则")
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if pending != "" {
		if err := s.loadFile(ctx, pending); err != nil {
			return err
		}
	}
	_, err := s.db.SQL.Exec(`UPDATE firewall_state SET pending_backup=NULL, confirm_deadline=NULL WHERE id=1`)
	return err
}

func (s *Service) checkFile(ctx context.Context, path string) error {
	if s.SkipNFT {
		return nil
	}
	r, err := s.run()(ctx, 10*time.Second, "/usr/sbin/nft", "-c", "-f", path)
	if err != nil {
		return fmt.Errorf("nft -c 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}

func (s *Service) loadFile(ctx context.Context, path string) error {
	if s.SkipNFT {
		return nil
	}
	r, err := s.run()(ctx, 10*time.Second, "/usr/sbin/nft", "-f", path)
	if err != nil {
		return fmt.Errorf("nft -f 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}

func (s *Service) dumpLive(ctx context.Context, dest string) error {
	if s.SkipNFT {
		return panelFS.WriteAtomic(dest, []byte("flush ruleset\n"), 0600)
	}
	r, err := s.run()(ctx, 10*time.Second, "/usr/sbin/nft", "list", "ruleset")
	if err != nil {
		return fmt.Errorf("nft list 失败：%s", exec.Clip(r.Stderr))
	}
	body := r.Stdout
	if strings.TrimSpace(body) == "" {
		body = "flush ruleset\n"
	}
	return panelFS.WriteAtomic(dest, []byte(body), 0600)
}

func (s *Service) Ban(ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("无效 IP")
	}
	if v4 := parsed.To4(); v4 != nil {
		ip = v4.String()
	}
	if s.Settings != nil {
		p, err := s.Settings.Public()
		if err == nil && len(p.OpsIPCIDRs) > 0 && settings.IPInCIDRs(ip, p.OpsIPCIDRs) {
			return nil
		}
	}
	if s.SkipNFT {
		return nil
	}
	elem := "{ " + ip + " timeout 30m }"
	r, err := s.run()(context.Background(), 5*time.Second, "/usr/sbin/nft", "add", "element", "inet", "lnmp_panel", "login_ban", elem)
	if err != nil {
		return fmt.Errorf("写入 login_ban 失败：%s", exec.Clip(r.Stderr+" "+r.Stdout))
	}
	return nil
}
