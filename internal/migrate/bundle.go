package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lnmp-panel/internal/firewall"
	panelFS "lnmp-panel/internal/fs"
	"lnmp-panel/internal/ssl"
)

type Bundle struct {
	Templates    []TplRow          `json:"templates"`
	Sites        []SiteRow         `json:"sites"`
	Domains      []DomRow          `json:"domains"`
	Redirects    []RedirRow        `json:"redirects"`
	Certs        []CertRow         `json:"certs"`
	PHP          []PHPRow          `json:"php"`
	MySQLDBs     []DBRow           `json:"mysql_dbs"`
	MySQLUsers   []UserRow         `json:"mysql_users"`
	Settings     map[string]string `json:"settings"`
	FirewallForm *firewall.Form    `json:"firewall_form,omitempty"`
	NginxGlobal  string            `json:"nginx_global"`
	MyCNF        string            `json:"my_cnf"`
}

type TplRow struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

type SiteRow struct {
	OldID           int64    `json:"old_id"`
	SiteKey         string   `json:"site_key"`
	Name            string   `json:"name"`
	Root            string   `json:"root"`
	PHPVersion      *string  `json:"php_version"`
	NginxTplHash    string   `json:"nginx_tpl_hash"`
	RewriteTplHash  string   `json:"rewrite_tpl_hash"`
	SSLTplHash      string   `json:"ssl_tpl_hash"`
	RawMode         bool     `json:"raw_mode"`
	Enabled         bool     `json:"enabled"`
	HTTP2           bool     `json:"http2"`
	HTTP3           bool     `json:"http3"`
	ProxyPass       string   `json:"proxy_pass"`
	AccessAllow     []string `json:"access_allow"`
	Hotlink         []string `json:"hotlink"`
	BasicAuthFile   string   `json:"basic_auth_file"`
	LastWrittenHash string   `json:"last_written_hash"`
	NginxConf       string   `json:"nginx_conf"`
}

type DomRow struct {
	SiteKey string `json:"site_key"`
	Domain  string `json:"domain"`
}

type RedirRow struct {
	SiteKey  string `json:"site_key"`
	FromPath string `json:"from_path"`
	ToURL    string `json:"to_url"`
	Code     int    `json:"code"`
}

type CertRow struct {
	SiteKey   string `json:"site_key"`
	Domains   string `json:"domains"`
	Issuer    string `json:"issuer"`
	ExpiresAt string `json:"expires_at"`
	Challenge string `json:"challenge"`
	CertPEM   string `json:"cert_pem"`
	KeyPEM    string `json:"key_pem"`
}

type PHPRow struct {
	Version    string   `json:"version"`
	Installed  bool     `json:"installed"`
	Extensions []string `json:"extensions"`
	INI        string   `json:"ini"`
	Pool       string   `json:"pool"`
}

type DBRow struct {
	Name    string `json:"name"`
	Charset string `json:"charset"`
	Collate string `json:"collate"`
}

type UserRow struct {
	User      string   `json:"user"`
	Host      string   `json:"host"`
	Databases []string `json:"databases"`
}

func (s *Service) exportBundle(ctx context.Context) (Bundle, error) {
	var b Bundle
	b.Settings = map[string]string{}
	for _, k := range []string{"metrics_raw_days", "metrics_hourly_days", "log_keep_days"} {
		if v, err := s.Settings.Get(k); err == nil && v != "" {
			b.Settings[k] = v
		}
	}
	tpls, err := s.Tpl.List("")
	if err != nil {
		return b, err
	}
	hashOf := map[int64]string{}
	for _, t := range tpls {
		hashOf[t.ID] = t.ContentHash
		if t.Origin != "user" {
			continue
		}
		b.Templates = append(b.Templates, TplRow{Type: t.Type, Name: t.Name, Content: t.Content, Hash: t.ContentHash})
	}
	sites, err := s.Sites.List()
	if err != nil {
		return b, err
	}
	for _, st := range sites {
		aa, hl := st.AccessAllow, st.Hotlink
		if aa == nil {
			aa = []string{}
		}
		if hl == nil {
			hl = []string{}
		}
		row := SiteRow{
			OldID: st.ID, SiteKey: st.SiteKey, Name: st.Name, Root: st.Root, PHPVersion: st.PHPVersion,
			RawMode: st.RawMode, Enabled: st.Enabled, HTTP2: st.HTTP2, HTTP3: st.HTTP3,
			ProxyPass: st.ProxyPass, AccessAllow: aa, Hotlink: hl, BasicAuthFile: st.BasicAuthFile,
			LastWrittenHash: st.LastWrittenHash,
		}
		if st.NginxTemplateID != nil {
			row.NginxTplHash = hashOf[*st.NginxTemplateID]
		}
		if st.RewriteTemplateID != nil {
			row.RewriteTplHash = hashOf[*st.RewriteTemplateID]
		}
		if st.SSLTemplateID != nil {
			row.SSLTplHash = hashOf[*st.SSLTemplateID]
		}
		if s.Nginx != nil {
			if c, err := s.Nginx.Read(st.SiteKey); err == nil {
				row.NginxConf = c
			}
		}
		b.Sites = append(b.Sites, row)
		for _, d := range st.Domains {
			b.Domains = append(b.Domains, DomRow{SiteKey: st.SiteKey, Domain: d})
		}
		for _, r := range st.Redirects {
			b.Redirects = append(b.Redirects, RedirRow{SiteKey: st.SiteKey, FromPath: r.FromPath, ToURL: r.ToURL, Code: r.Code})
		}
		if st.SSLCert != "" {
			certPEM, _ := os.ReadFile(st.SSLCert)
			keyPEM, _ := os.ReadFile(st.SSLKey)
			if len(certPEM) > 0 {
				exp := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
				if t, err := ssl.LeafNotAfter(certPEM); err == nil {
					exp = t.UTC().Format(time.RFC3339)
				}
				b.Certs = append(b.Certs, CertRow{
					SiteKey: st.SiteKey, Issuer: "copied", Challenge: "http-01",
					Domains: strings.Join(st.Domains, ","),
					CertPEM: string(certPEM), KeyPEM: string(keyPEM),
					ExpiresAt: exp,
				})
			}
		}
	}
	if s.PHP != nil {
		vers, _ := s.PHP.List(ctx)
		for _, v := range vers {
			if !v.Installed {
				continue
			}
			pr := PHPRow{Version: v.Version, Installed: true, Extensions: v.Extensions}
			if raw, err := os.ReadFile(filepath.Join("/etc/php", v.Version, "fpm", "conf.d", "99-lnmp-panel.ini")); err == nil {
				pr.INI = string(raw)
			}
			if raw, err := os.ReadFile(filepath.Join("/etc/php", v.Version, "fpm", "pool.d", "lnmp-panel.conf")); err == nil {
				pr.Pool = string(raw)
			}
			b.PHP = append(b.PHP, pr)
		}
	}
	if s.MySQL != nil {
		dbs, _ := s.MySQL.ListDB()
		b.MySQLDBs = make([]DBRow, 0, len(dbs))
		for _, d := range dbs {
			b.MySQLDBs = append(b.MySQLDBs, DBRow{Name: d.Name, Charset: d.Charset, Collate: d.Collate})
		}
		users, _ := s.MySQL.ListUsers()
		for _, u := range users {
			b.MySQLUsers = append(b.MySQLUsers, UserRow{User: u.User, Host: u.Host, Databases: u.Databases})
		}
	}
	if s.Firewall != nil {
		f := s.Firewall.Form()
		b.FirewallForm = &f
	}
	if s.Nginx != nil && s.Nginx.ConfD != "" {
		if raw, err := os.ReadFile(filepath.Join(s.Nginx.ConfD, "lnmp-panel.conf")); err == nil {
			b.NginxGlobal = string(raw)
		}
	}
	if s.MySQL != nil && s.MySQL.CNFPath != "" {
		if raw, err := os.ReadFile(s.MySQL.CNFPath); err == nil {
			b.MyCNF = string(raw)
		}
	}
	if b.Templates == nil {
		b.Templates = []TplRow{}
	}
	if b.Sites == nil {
		b.Sites = []SiteRow{}
	}
	return b, nil
}

var tlsSiteDir = regexp.MustCompile(`tls/sites/\d+/`)

func rewriteCertDir(conf string, newID int64) string {
	return tlsSiteDir.ReplaceAllString(conf, fmt.Sprintf("tls/sites/%d/", newID))
}

func (s *Service) applyBundle(ctx context.Context, b Bundle) ([]string, error) {
	var missing []string
	hashToID := map[string]int64{}
	if s.Tpl != nil {
		official, err := s.Tpl.List("")
		if err != nil {
			return nil, err
		}
		for _, t := range official {
			hashToID[t.ContentHash] = t.ID
		}
		for _, t := range b.Templates {
			id, err := s.Tpl.EnsureUser(t.Type, t.Content, nil)
			if err != nil {
				return missing, fmt.Errorf("用户模板 %s：%w", t.Name, err)
			}
			hashToID[t.Hash] = id
			if t.Name != "" {
				_, _ = s.db.SQL.Exec(`UPDATE templates SET name=? WHERE id=? AND origin='user'`, t.Name, id)
			}
		}
	}
	idByKey := map[string]int64{}
	keep := map[string]bool{}
	for _, st := range b.Sites {
		keep[st.SiteKey] = true
		var nid, rid, sid *int64
		if id, ok := hashToID[st.NginxTplHash]; ok {
			nid = &id
		}
		if id, ok := hashToID[st.RewriteTplHash]; ok {
			rid = &id
		}
		if id, ok := hashToID[st.SSLTplHash]; ok {
			sid = &id
		}
		var existing int64
		err := s.db.SQL.QueryRow(`SELECT id FROM sites WHERE site_key=?`, st.SiteKey).Scan(&existing)
		now := s.db.Now()
		aa, hl := st.AccessAllow, st.Hotlink
		if aa == nil {
			aa = []string{}
		}
		if hl == nil {
			hl = []string{}
		}
		ab, _ := json.Marshal(aa)
		hb, _ := json.Marshal(hl)
		ba := st.BasicAuthFile
		if errorsIsNoRows(err) {
			res, err := s.db.SQL.Exec(`INSERT INTO sites(site_key, name, root, php_version, nginx_template_id, rewrite_template_id, ssl_template_id,
				raw_mode, last_written_hash, enabled, http2, http3, proxy_pass, access_allow, hotlink, basic_auth_file, created_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				st.SiteKey, st.Name, st.Root, st.PHPVersion, nid, rid, sid, boolInt(st.RawMode), st.LastWrittenHash,
				boolInt(st.Enabled), boolInt(st.HTTP2), boolInt(st.HTTP3), st.ProxyPass, string(ab), string(hb), ba, now, now)
			if err != nil {
				return missing, err
			}
			existing, _ = res.LastInsertId()
		} else if err != nil {
			return missing, err
		} else {
			_, err = s.db.SQL.Exec(`UPDATE sites SET name=?, root=?, php_version=?, nginx_template_id=?, rewrite_template_id=?, ssl_template_id=?,
				raw_mode=?, last_written_hash=?, enabled=?, http2=?, http3=?, proxy_pass=?, access_allow=?, hotlink=?, basic_auth_file=?, updated_at=? WHERE id=?`,
				st.Name, st.Root, st.PHPVersion, nid, rid, sid, boolInt(st.RawMode), st.LastWrittenHash,
				boolInt(st.Enabled), boolInt(st.HTTP2), boolInt(st.HTTP3), st.ProxyPass, string(ab), string(hb), ba, now, existing)
			if err != nil {
				return missing, err
			}
		}
		idByKey[st.SiteKey] = existing
		_, _ = s.db.SQL.Exec(`DELETE FROM site_domains WHERE site_id=?`, existing)
		_, _ = s.db.SQL.Exec(`DELETE FROM site_redirects WHERE site_id=?`, existing)
	}
	for _, d := range b.Domains {
		if id, ok := idByKey[d.SiteKey]; ok {
			_, _ = s.db.SQL.Exec(`INSERT INTO site_domains(domain, site_id) VALUES (?,?)`, d.Domain, id)
		}
	}
	for _, r := range b.Redirects {
		if id, ok := idByKey[r.SiteKey]; ok {
			_, _ = s.db.SQL.Exec(`INSERT INTO site_redirects(site_id, from_path, to_url, code) VALUES (?,?,?,?)`, id, r.FromPath, r.ToURL, r.Code)
		}
	}
	for _, c := range b.Certs {
		id, ok := idByKey[c.SiteKey]
		if !ok || s.SSL == nil || s.SSL.Dir == "" {
			continue
		}
		dir := filepath.Join(s.SSL.Dir, fmt.Sprintf("%d", id))
		_ = os.MkdirAll(dir, 0700)
		certPath := filepath.Join(dir, "fullchain.pem")
		keyPath := filepath.Join(dir, "privkey.pem")
		_ = panelFS.WriteAtomic(certPath, []byte(c.CertPEM), 0644)
		_ = panelFS.WriteAtomic(keyPath, []byte(c.KeyPEM), 0600)
		_, _ = s.db.SQL.Exec(`DELETE FROM certificates WHERE site_id=?`, id)
		exp := c.ExpiresAt
		if t, err := ssl.LeafNotAfter([]byte(c.CertPEM)); err == nil {
			exp = t.UTC().Format(time.RFC3339)
		}
		_, _ = s.db.SQL.Exec(`INSERT INTO certificates(site_id, domains, issuer, cert_path, key_path, expires_at, challenge, created_at)
			VALUES (?,?,?,?,?,?,?,?)`, id, c.Domains, c.Issuer, certPath, keyPath, exp, c.Challenge, s.db.Now())
	}
	for _, st := range b.Sites {
		id := idByKey[st.SiteKey]
		if s.Nginx == nil {
			continue
		}
		if st.RawMode {
			conf := rewriteCertDir(st.NginxConf, id)
			_ = s.Nginx.WriteSite(st.SiteKey, conf)
			if st.Enabled {
				_ = s.Nginx.Enable(st.SiteKey)
			}
			continue
		}
		if s.Sites != nil {
			if err := s.Sites.ApplyConf(ctx, id); err != nil {
				return missing, fmt.Errorf("渲染站点 %s：%w", st.SiteKey, err)
			}
		}
	}
	rows, err := s.db.SQL.Query(`SELECT id, site_key FROM sites`)
	if err == nil {
		var drop []struct {
			id  int64
			key string
		}
		for rows.Next() {
			var id int64
			var key string
			if rows.Scan(&id, &key) == nil && !keep[key] {
				drop = append(drop, struct {
					id  int64
					key string
				}{id, key})
			}
		}
		_ = rows.Close()
		for _, d := range drop {
			_, _ = s.db.SQL.Exec(`DELETE FROM certificates WHERE site_id=?`, d.id)
			_, _ = s.db.SQL.Exec(`DELETE FROM sites WHERE id=?`, d.id)
			_ = s.Nginx.RemoveSite(d.key)
		}
	}
	if s.Settings != nil {
		for k, v := range b.Settings {
			_ = s.Settings.Set(k, v)
		}
	}
	for _, p := range b.PHP {
		ext, _ := json.Marshal(p.Extensions)
		if len(ext) == 0 || string(ext) == "null" {
			ext = []byte("[]")
		}
		installed := 0
		if s.PHP != nil && s.PHP.BinaryInstalled(p.Version) {
			installed = 1
		} else {
			missing = append(missing, "PHP "+p.Version)
		}
		_, _ = s.db.SQL.Exec(`INSERT INTO php_versions(version, installed, extensions) VALUES (?,?,?)
			ON CONFLICT(version) DO UPDATE SET installed=excluded.installed, extensions=excluded.extensions`,
			p.Version, installed, string(ext))
		ini := filepath.Join("/etc/php", p.Version, "fpm", "conf.d", "99-lnmp-panel.ini")
		pool := filepath.Join("/etc/php", p.Version, "fpm", "pool.d", "lnmp-panel.conf")
		if p.INI != "" {
			_ = panelFS.Mkdir(filepath.Dir(ini), 0755)
			_ = panelFS.WriteAtomic(ini, []byte(p.INI), 0644)
		}
		if p.Pool != "" {
			_ = panelFS.Mkdir(filepath.Dir(pool), 0755)
			_ = panelFS.WriteAtomic(pool, []byte(p.Pool), 0644)
		}
	}
	if b.FirewallForm != nil && s.Firewall != nil {
		if err := s.Firewall.StageMigrated(*b.FirewallForm); err != nil {
			return missing, fmt.Errorf("防火墙草稿：%w", err)
		}
	}
	if b.NginxGlobal != "" && s.Nginx != nil && s.Nginx.ConfD != "" {
		_ = panelFS.WriteAtomic(filepath.Join(s.Nginx.ConfD, "lnmp-panel.conf"), []byte(b.NginxGlobal), 0644)
	}
	if b.MyCNF != "" && s.MySQL != nil && s.MySQL.CNFPath != "" {
		_ = panelFS.Mkdir(filepath.Dir(s.MySQL.CNFPath), 0755)
		_ = panelFS.WriteAtomic(s.MySQL.CNFPath, []byte(b.MyCNF), 0644)
	}
	for _, d := range b.MySQLDBs {
		var n int
		_ = s.db.SQL.QueryRow(`SELECT COUNT(1) FROM mysql_databases WHERE name=?`, d.Name).Scan(&n)
		if n == 0 {
			_, _ = s.db.SQL.Exec(`INSERT INTO mysql_databases(name, charset, "collate", created_at) VALUES (?,?,?,?)`,
				d.Name, d.Charset, d.Collate, s.db.Now())
		}
	}
	for _, u := range b.MySQLUsers {
		_, _ = s.db.SQL.Exec(`INSERT INTO mysql_users(user, host, created_at) VALUES (?,?,?) ON CONFLICT(user, host) DO NOTHING`,
			u.User, u.Host, s.db.Now())
		var uid int64
		if s.db.SQL.QueryRow(`SELECT id FROM mysql_users WHERE user=? AND host=?`, u.User, u.Host).Scan(&uid) != nil {
			continue
		}
		_, _ = s.db.SQL.Exec(`DELETE FROM mysql_grants WHERE user_id=?`, uid)
		for _, dbn := range u.Databases {
			_, _ = s.db.SQL.Exec(`INSERT INTO mysql_grants(user_id, db_name, priv) VALUES (?,?, 'ALL')`, uid, dbn)
		}
	}
	if s.Nginx != nil {
		_ = s.Nginx.EnsureLayout()
		if !s.Nginx.Installed() {
			missing = append(missing, "Nginx")
		} else if !s.SkipExec {
			if err := s.Nginx.TestAndReload(ctx); err != nil {
				return missing, fmt.Errorf("nginx 重载：%w", err)
			}
		}
	}
	if len(b.MySQLDBs) > 0 && (s.MySQL == nil || !s.MySQL.Installed()) {
		missing = append(missing, "MySQL")
	}
	return missing, nil
}

func errorsIsNoRows(err error) bool {
	return err != nil && (err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows"))
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
