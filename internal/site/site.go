package site

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"lnmp-panel/internal/nginx"
	"lnmp-panel/internal/store"
	"lnmp-panel/internal/template"
)

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var (
	ErrNotFound = errors.New("站点不存在")
	ErrDrift    = errors.New("磁盘配置与面板上次写入不一致，请确认后带 force=true 保存")
	ErrDomain   = errors.New("域名已被其他站点使用")
	ErrKey      = errors.New("site_key 须为小写字母数字和短横线")
	ErrRawMode  = errors.New("站点处于原文模式，请先恢复为模板渲染")
	ErrPurge    = errors.New("站点未删除：根目录不在 /var/www 下，请去掉 purge 选项或手动清理")
)

type Service struct {
	db      *store.DB
	tpl     *template.Service
	ngx     *nginx.Service
	WWW     string
	CertDir string
	SkipNX  bool // 仅测试显式打开，禁止从环境推断
}

func New(db *store.DB, tpl *template.Service, ngx *nginx.Service) *Service {
	return &Service{db: db, tpl: tpl, ngx: ngx, WWW: "/var/www", CertDir: "/var/lib/lnmp-panel/tls/sites"}
}

type Redirect struct {
	ID       int64  `json:"id"`
	FromPath string `json:"from_path"`
	ToURL    string `json:"to_url"`
	Code     int    `json:"code"`
}

type Site struct {
	ID                int64      `json:"id"`
	SiteKey           string     `json:"site_key"`
	Name              string     `json:"name"`
	Domains           []string   `json:"domains"`
	Root              string     `json:"root"`
	PHPVersion        *string    `json:"php_version"`
	NginxTemplateID   *int64     `json:"nginx_template_id"`
	RewriteTemplateID *int64     `json:"rewrite_template_id"`
	SSLTemplateID     *int64     `json:"ssl_template_id"`
	RawMode           bool       `json:"raw_mode"`
	Enabled           bool       `json:"enabled"`
	HTTP2             bool       `json:"http2"`
	HTTP3             bool       `json:"http3"`
	ProxyPass         string     `json:"proxy_pass"`
	AccessAllow       []string   `json:"access_allow"`
	Hotlink           []string   `json:"hotlink"`
	BasicAuthFile     string     `json:"basic_auth_file"`
	Drifted           bool       `json:"drifted"`
	LastWrittenHash   string     `json:"last_written_hash"`
	Redirects         []Redirect `json:"redirects"`
	SSLCert           string     `json:"ssl_cert"`
	SSLKey            string     `json:"ssl_key"`
	CreatedAt         string     `json:"created_at"`
	UpdatedAt         string     `json:"updated_at"`
}

type In struct {
	SiteKey           string   `json:"site_key"`
	Name              string   `json:"name"`
	Domains           []string `json:"domains"`
	Root              string   `json:"root"`
	PHPVersion        *string  `json:"php_version"`
	NginxTemplateID   *int64   `json:"nginx_template_id"`
	RewriteTemplateID *int64   `json:"rewrite_template_id"`
	SSLTemplateID     *int64   `json:"ssl_template_id"`
	HTTP2             *bool    `json:"http2"`
	HTTP3             *bool    `json:"http3"`
	ProxyPass         *string  `json:"proxy_pass"`
	AccessAllow       []string `json:"access_allow"`
	Hotlink           []string `json:"hotlink"`
	BasicAuthFile     *string  `json:"basic_auth_file"`
	Force             bool     `json:"force"`
}

func (s *Service) List() ([]Site, error) {
	rows, err := s.db.SQL.Query(`SELECT id FROM sites ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		st, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if out == nil {
		out = []Site{}
	}
	return out, rows.Err()
}

func (s *Service) Get(id int64) (Site, error) {
	var st Site
	var php, hash sql.NullString
	var nT, rT, sT sql.NullInt64
	var raw, en, h2, h3 int
	var access, hot string
	err := s.db.SQL.QueryRow(`SELECT id, site_key, name, root, php_version, nginx_template_id, rewrite_template_id, ssl_template_id,
		raw_mode, last_written_hash, enabled, http2, http3, proxy_pass, access_allow, hotlink, basic_auth_file, created_at, updated_at
		FROM sites WHERE id=?`, id).Scan(&st.ID, &st.SiteKey, &st.Name, &st.Root, &php, &nT, &rT, &sT,
		&raw, &hash, &en, &h2, &h3, &st.ProxyPass, &access, &hot, &st.BasicAuthFile, &st.CreatedAt, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	if err != nil {
		return Site{}, err
	}
	if php.Valid {
		st.PHPVersion = &php.String
	}
	if nT.Valid {
		st.NginxTemplateID = &nT.Int64
	}
	if rT.Valid {
		st.RewriteTemplateID = &rT.Int64
	}
	if sT.Valid {
		st.SSLTemplateID = &sT.Int64
	}
	st.RawMode = raw == 1
	st.Enabled = en == 1
	st.HTTP2 = h2 == 1
	st.HTTP3 = h3 == 1
	st.LastWrittenHash = hash.String
	_ = json.Unmarshal([]byte(access), &st.AccessAllow)
	_ = json.Unmarshal([]byte(hot), &st.Hotlink)
	if st.AccessAllow == nil {
		st.AccessAllow = []string{}
	}
	if st.Hotlink == nil {
		st.Hotlink = []string{}
	}
	st.Domains = s.domains(id)
	st.Redirects = s.redirects(id)
	st.SSLCert, st.SSLKey = s.liveCert(id)
	if disk, err := s.ngx.HashOf(st.SiteKey); err == nil && st.LastWrittenHash != "" {
		st.Drifted = disk != st.LastWrittenHash
	}
	return st, nil
}

func (s *Service) domains(id int64) []string {
	rows, err := s.db.SQL.Query(`SELECT domain FROM site_domains WHERE site_id=? ORDER BY domain`, id)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		out = append(out, d)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (s *Service) redirects(id int64) []Redirect {
	rows, err := s.db.SQL.Query(`SELECT id, from_path, to_url, code FROM site_redirects WHERE site_id=? ORDER BY id`, id)
	if err != nil {
		return []Redirect{}
	}
	defer rows.Close()
	var out []Redirect
	for rows.Next() {
		var r Redirect
		_ = rows.Scan(&r.ID, &r.FromPath, &r.ToURL, &r.Code)
		out = append(out, r)
	}
	if out == nil {
		out = []Redirect{}
	}
	return out
}

func (s *Service) liveCert(id int64) (string, string) {
	var c, k string
	err := s.db.SQL.QueryRow(`SELECT cert_path, key_path FROM certificates WHERE site_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&c, &k)
	if err != nil {
		return "", ""
	}
	return c, k
}

func (s *Service) Create(ctx context.Context, in In) (Site, error) {
	st, err := s.prepare(in)
	if err != nil {
		return Site{}, err
	}
	if err := s.checkDomains(0, st.Domains); err != nil {
		return Site{}, err
	}
	content, err := s.render(st)
	if err != nil {
		return Site{}, err
	}
	if err := s.ensureRoot(st.Root); err != nil {
		return Site{}, err
	}
	if err := s.ngx.WriteSite(st.SiteKey, content); err != nil {
		return Site{}, err
	}
	if err := s.ngx.Enable(st.SiteKey); err != nil {
		_ = s.ngx.RemoveSite(st.SiteKey)
		return Site{}, err
	}
	if !s.SkipNX {
		if err := s.ngx.TestAndReload(ctx); err != nil {
			_ = s.ngx.RemoveSite(st.SiteKey)
			return Site{}, err
		}
	}
	h := template.Hash(content)
	if disk, err := s.ngx.HashOf(st.SiteKey); err == nil {
		h = disk
	}
	ab, _ := json.Marshal(st.AccessAllow)
	hb, _ := json.Marshal(st.Hotlink)
	now := s.db.Now()
	res, err := s.db.SQL.Exec(`INSERT INTO sites(site_key, name, root, php_version, nginx_template_id, rewrite_template_id, ssl_template_id,
		raw_mode, last_written_hash, enabled, http2, http3, proxy_pass, access_allow, hotlink, basic_auth_file, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,0,?,1,?,?,?,?,?,?,?,?)`,
		st.SiteKey, st.Name, st.Root, st.PHPVersion, st.NginxTemplateID, st.RewriteTemplateID, st.SSLTemplateID, h,
		boolInt(st.HTTP2), boolInt(st.HTTP3), st.ProxyPass, string(ab), string(hb), st.BasicAuthFile, now, now)
	if err != nil {
		_ = s.ngx.RemoveSite(st.SiteKey)
		return Site{}, err
	}
	id, _ := res.LastInsertId()
	if err := s.replaceDomains(id, st.Domains); err != nil {
		_, _ = s.db.SQL.Exec(`DELETE FROM sites WHERE id=?`, id)
		_ = s.ngx.RemoveSite(st.SiteKey)
		return Site{}, err
	}
	return s.Get(id)
}

func (s *Service) Update(ctx context.Context, id int64, in In) (Site, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Site{}, err
	}
	if cur.RawMode {
		return Site{}, ErrRawMode
	}
	if cur.Drifted && !in.Force {
		return Site{}, ErrDrift
	}
	if len(in.Domains) > 0 {
		for i, d := range in.Domains {
			d = strings.ToLower(strings.TrimSpace(d))
			if err := validDomain(d); err != nil {
				return Site{}, err
			}
			in.Domains[i] = d
		}
		if err := s.checkDomains(id, in.Domains); err != nil {
			return Site{}, err
		}
	}
	name, root := cur.Name, cur.Root
	if in.Name != "" {
		name = in.Name
	}
	if in.Root != "" {
		root = in.Root
	}
	php := cur.PHPVersion
	if in.PHPVersion != nil {
		php = in.PHPVersion
	}
	nid, rid, sid := cur.NginxTemplateID, cur.RewriteTemplateID, cur.SSLTemplateID
	if in.NginxTemplateID != nil {
		nid = in.NginxTemplateID
	}
	if in.RewriteTemplateID != nil {
		rid = in.RewriteTemplateID
	}
	if in.SSLTemplateID != nil {
		sid = in.SSLTemplateID
	}
	h2, h3, pp, ba := cur.HTTP2, cur.HTTP3, cur.ProxyPass, cur.BasicAuthFile
	if in.HTTP2 != nil {
		h2 = *in.HTTP2
	}
	if in.HTTP3 != nil {
		h3 = *in.HTTP3
	}
	if in.ProxyPass != nil {
		pp = *in.ProxyPass
	}
	if in.BasicAuthFile != nil {
		ba = *in.BasicAuthFile
	}
	access, hot := cur.AccessAllow, cur.Hotlink
	if in.AccessAllow != nil {
		access = in.AccessAllow
	}
	if in.Hotlink != nil {
		hot = in.Hotlink
	}
	ab, _ := json.Marshal(nz(access))
	hb, _ := json.Marshal(nz(hot))
	if err := validRoot(root); err != nil {
		return Site{}, err
	}
	if err := validToken("反代目标", pp); err != nil {
		return Site{}, err
	}
	if err := validToken("basic auth 文件", ba); err != nil {
		return Site{}, err
	}
	for _, x := range access {
		if err := validToken("访问限制", x); err != nil {
			return Site{}, err
		}
	}
	for _, x := range hot {
		if err := validToken("防盗链", x); err != nil {
			return Site{}, err
		}
	}
	_, err = s.db.SQL.Exec(`UPDATE sites SET name=?, root=?, php_version=?, nginx_template_id=?, rewrite_template_id=?, ssl_template_id=?,
		http2=?, http3=?, proxy_pass=?, access_allow=?, hotlink=?, basic_auth_file=?, updated_at=? WHERE id=?`,
		name, root, php, nid, rid, sid, boolInt(h2), boolInt(h3), pp, string(ab), string(hb), ba, s.db.Now(), id)
	if err != nil {
		return Site{}, err
	}
	if len(in.Domains) > 0 {
		if err := s.replaceDomains(id, in.Domains); err != nil {
			return Site{}, err
		}
	}
	if err := s.apply(ctx, id, false); err != nil {
		return Site{}, err
	}
	return s.Get(id)
}

func (s *Service) Delete(ctx context.Context, id int64, purge bool) error {
	st, err := s.Get(id)
	if err != nil {
		return err
	}
	if purge && !s.underWWW(st.Root) {
		return ErrPurge
	}
	tx, err := s.db.SQL.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM certificates WHERE site_id=?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sites WHERE id=?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = s.ngx.Disable(st.SiteKey)
	_ = s.ngx.RemoveSite(st.SiteKey)
	_ = os.RemoveAll(filepath.Join(s.CertDir, fmt.Sprintf("%d", id)))
	if purge {
		_ = os.RemoveAll(st.Root)
	}
	if !s.SkipNX {
		_ = s.ngx.TestAndReload(ctx)
	}
	return nil
}

func (s *Service) SetEnabled(ctx context.Context, id int64, on bool) error {
	st, err := s.Get(id)
	if err != nil {
		return err
	}
	if on {
		if err := s.ngx.Enable(st.SiteKey); err != nil {
			return err
		}
	} else {
		_ = s.ngx.Disable(st.SiteKey)
	}
	_, _ = s.db.SQL.Exec(`UPDATE sites SET enabled=?, updated_at=? WHERE id=?`, boolInt(on), s.db.Now(), id)
	if !s.SkipNX {
		return s.ngx.TestAndReload(ctx)
	}
	return nil
}

func (s *Service) PutRaw(ctx context.Context, id int64, body string, force bool) error {
	st, err := s.Get(id)
	if err != nil {
		return err
	}
	if st.Drifted && !force {
		return ErrDrift
	}
	old, _ := s.ngx.Read(st.SiteKey)
	if err := s.ngx.WriteSite(st.SiteKey, body); err != nil {
		return err
	}
	if !s.SkipNX {
		if err := s.ngx.TestAndReload(ctx); err != nil {
			if old != "" {
				_ = s.ngx.WriteSite(st.SiteKey, old)
			}
			return err
		}
	}
	h, _ := s.ngx.HashOf(st.SiteKey)
	tid, _ := s.tpl.EnsureUser("nginx_site", body, st.NginxTemplateID)
	_, err = s.db.SQL.Exec(`UPDATE sites SET raw_mode=1, nginx_template_id=?, last_written_hash=?, updated_at=? WHERE id=?`,
		tid, h, s.db.Now(), id)
	return err
}

func (s *Service) UseTemplate(ctx context.Context, id int64) error {
	_, err := s.Get(id)
	if err != nil {
		return err
	}
	_, err = s.db.SQL.Exec(`UPDATE sites SET raw_mode=0, updated_at=? WHERE id=?`, s.db.Now(), id)
	if err != nil {
		return err
	}
	return s.apply(ctx, id, true)
}

func (s *Service) PutRedirects(ctx context.Context, id int64, rs []Redirect) error {
	st, err := s.Get(id)
	if err != nil {
		return err
	}
	if st.RawMode {
		return ErrRawMode
	}
	for _, r := range rs {
		if err := validToken("跳转路径", r.FromPath); err != nil {
			return err
		}
		if err := validToken("跳转目标", r.ToURL); err != nil {
			return err
		}
	}
	_, _ = s.db.SQL.Exec(`DELETE FROM site_redirects WHERE site_id=?`, id)
	for _, r := range rs {
		if r.Code == 0 {
			r.Code = 301
		}
		_, _ = s.db.SQL.Exec(`INSERT INTO site_redirects(site_id, from_path, to_url, code) VALUES (?,?,?,?)`,
			id, r.FromPath, r.ToURL, r.Code)
	}
	return s.apply(ctx, id, false)
}

func (s *Service) ApplyConf(ctx context.Context, id int64) error {
	return s.apply(ctx, id, true)
}

func (s *Service) render(st Site) (string, error) {
	if st.NginxTemplateID == nil {
		return "", fmt.Errorf("未选择 nginx 模板")
	}
	v := nginx.Vars{
		SiteKey: st.SiteKey, Root: st.Root, Domains: st.Domains,
		HTTP2: st.HTTP2, HTTP3: st.HTTP3, ProxyPass: st.ProxyPass,
		AccessAllow: st.AccessAllow, Hotlink: st.Hotlink, BasicAuthFile: st.BasicAuthFile,
		SSLCert: st.SSLCert, SSLKey: st.SSLKey,
	}
	if st.PHPVersion != nil && *st.PHPVersion != "" {
		v.PHPSocket = "/run/php/php" + *st.PHPVersion + "-fpm.sock"
	}
	for _, r := range st.Redirects {
		v.Redirects = append(v.Redirects, nginx.Redirect{FromPath: r.FromPath, ToURL: r.ToURL, Code: r.Code})
	}
	var rid, sid int64
	if st.RewriteTemplateID != nil {
		rid = *st.RewriteTemplateID
	}
	if st.SSLTemplateID != nil {
		sid = *st.SSLTemplateID
	}
	return nginx.RenderSite(s.tpl, *st.NginxTemplateID, rid, sid, v)
}

func (s *Service) apply(ctx context.Context, id int64, force bool) error {
	st, err := s.Get(id)
	if err != nil {
		return err
	}
	if st.RawMode && !force {
		return nil
	}
	content, err := s.render(st)
	if err != nil {
		return err
	}
	old, _ := s.ngx.Read(st.SiteKey)
	if err := s.ngx.WriteSite(st.SiteKey, content); err != nil {
		return err
	}
	if st.Enabled {
		_ = s.ngx.Enable(st.SiteKey)
	}
	if !s.SkipNX {
		if err := s.ngx.TestAndReload(ctx); err != nil {
			if old != "" {
				_ = s.ngx.WriteSite(st.SiteKey, old)
			}
			return err
		}
	}
	h := template.Hash(content)
	if disk, err := s.ngx.HashOf(st.SiteKey); err == nil {
		h = disk
	}
	_, err = s.db.SQL.Exec(`UPDATE sites SET last_written_hash=?, updated_at=? WHERE id=?`, h, s.db.Now(), id)
	return err
}

func (s *Service) prepare(in In) (Site, error) {
	if !keyRe.MatchString(in.SiteKey) {
		return Site{}, ErrKey
	}
	if in.Name == "" {
		in.Name = in.SiteKey
	}
	if len(in.Domains) == 0 {
		return Site{}, fmt.Errorf("至少填写一个域名")
	}
	var domains []string
	for _, d := range in.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if err := validDomain(d); err != nil {
			return Site{}, err
		}
		domains = append(domains, d)
	}
	if in.Root == "" {
		in.Root = filepath.Join(s.WWW, in.SiteKey)
	}
	if err := validRoot(in.Root); err != nil {
		return Site{}, err
	}
	h2, h3 := true, false
	if in.HTTP2 != nil {
		h2 = *in.HTTP2
	}
	if in.HTTP3 != nil {
		h3 = *in.HTTP3
	}
	pp, ba := "", ""
	if in.ProxyPass != nil {
		pp = *in.ProxyPass
	}
	if in.BasicAuthFile != nil {
		ba = *in.BasicAuthFile
	}
	if err := validToken("反代目标", pp); err != nil {
		return Site{}, err
	}
	if err := validToken("basic auth 文件", ba); err != nil {
		return Site{}, err
	}
	for _, x := range in.AccessAllow {
		if err := validToken("访问限制", x); err != nil {
			return Site{}, err
		}
	}
	for _, x := range in.Hotlink {
		if err := validToken("防盗链", x); err != nil {
			return Site{}, err
		}
	}
	if in.PHPVersion != nil && *in.PHPVersion != "" {
		if !phpVerRe.MatchString(*in.PHPVersion) {
			return Site{}, fmt.Errorf("无效 PHP 版本（需要 8.2 及以上）")
		}
	}
	nid, rid, sid, err := s.defaultTpls(in)
	if err != nil {
		return Site{}, err
	}
	return Site{
		SiteKey: in.SiteKey, Name: in.Name, Domains: domains, Root: in.Root,
		PHPVersion: in.PHPVersion, NginxTemplateID: nid, RewriteTemplateID: rid, SSLTemplateID: sid,
		Enabled: true, HTTP2: h2, HTTP3: h3, ProxyPass: pp, AccessAllow: nz(in.AccessAllow),
		Hotlink: nz(in.Hotlink), BasicAuthFile: ba,
	}, nil
}

func (s *Service) defaultTpls(in In) (*int64, *int64, *int64, error) {
	nid := in.NginxTemplateID
	if nid == nil {
		name := "静态站点"
		if in.PHPVersion != nil && *in.PHPVersion != "" {
			name = "PHP 站点"
		}
		if in.ProxyPass != nil && *in.ProxyPass != "" {
			name = "反向代理"
		}
		r, err := s.tpl.OfficialByName("nginx_site", name)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("缺少官方模板 %s", name)
		}
		nid = &r.ID
	}
	rid := in.RewriteTemplateID
	sid := in.SSLTemplateID
	if sid == nil {
		if r, err := s.tpl.OfficialByName("nginx_ssl", "现代 TLS"); err == nil {
			sid = &r.ID
		}
	}
	return nid, rid, sid, nil
}

func (s *Service) checkDomains(exceptID int64, domains []string) error {
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			return fmt.Errorf("域名不能为空")
		}
		var other int64
		err := s.db.SQL.QueryRow(`SELECT site_id FROM site_domains WHERE domain=?`, d).Scan(&other)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if other != exceptID {
			return ErrDomain
		}
	}
	return nil
}

func (s *Service) replaceDomains(id int64, domains []string) error {
	_, _ = s.db.SQL.Exec(`DELETE FROM site_domains WHERE site_id=?`, id)
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if _, err := s.db.SQL.Exec(`INSERT INTO site_domains(domain, site_id) VALUES (?,?)`, d, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ensureRoot(root string) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	if err := os.Chmod(root, 0755); err != nil {
		return err
	}
	idx := filepath.Join(root, "index.html")
	if _, err := os.Stat(idx); os.IsNotExist(err) {
		if err := os.WriteFile(idx, []byte("<!doctype html><meta charset=utf-8><title>LNMP</title><p>站点已创建。</p>\n"), 0644); err != nil {
			return err
		}
	}
	_ = os.Chmod(idx, 0644)
	if u, err := user.Lookup("www-data"); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		_ = os.Chown(root, uid, gid)
		_ = os.Chown(idx, uid, gid)
	}
	return nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
