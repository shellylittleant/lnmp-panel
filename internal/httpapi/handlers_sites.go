package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"lnmp-panel/internal/mysql"
	"lnmp-panel/internal/php"
	"lnmp-panel/internal/site"
)

func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request) {
	ngx := false
	ver := ""
	if s.Nginx != nil {
		ngx = s.Nginx.Installed()
		ver = s.Nginx.Version(r.Context())
	}
	phpLine, mysqlLine := "未安装", "未安装"
	phpPPA, mysqlOn := false, false
	if s.PHP != nil {
		phpLine = s.PHP.StatusLine()
		if s.PHP.PPACheck != nil {
			phpPPA = s.PHP.PPACheck()
		} else {
			phpPPA = php.PPAPresent()
		}
	}
	mysqlSource := "ubuntu"
	if s.Stack != nil {
		mysqlSource = s.Stack.MySQLSource()
	}
	if s.MySQL != nil {
		mysqlLine = s.MySQL.StatusLine(r.Context())
		mysqlOn = s.MySQL.Installed()
	}
	writeOK(w, map[string]any{
		"nginx_installed":       ngx,
		"nginx_version":         ver,
		"php":                   phpLine,
		"php_ppa":               phpPPA,
		"mysql":                 mysqlLine,
		"mysql_source":          mysqlSource,
		"mysql_official_source": mysql.OfficialPresent(),
		"mysql_installed":       mysqlOn,
	})
}

func (s *Server) handleNginxInstall(w http.ResponseWriter, r *http.Request) {
	if s.Installer == nil {
		writeErr(w, 500, "internal", "安装器未初始化")
		return
	}
	id, err := s.Installer.Start(r.Context())
	if err != nil {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	writeOK(w, map[string]any{"job_id": id})
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := s.Tpl.List(r.URL.Query().Get("type"))
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleTemplateGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rec, err := s.Tpl.Get(id)
	if err != nil {
		writeErr(w, 404, "not_found", "模板不存在")
		return
	}
	writeOK(w, rec)
}

func (s *Server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var raw map[string]string
	if err := readJSON(w, r, &raw); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	id, err := s.Tpl.CreateUser(raw["type"], raw["name"], raw["content"], nil)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	rec, _ := s.Tpl.Get(id)
	writeOK(w, rec)
}

func (s *Server) handleTemplatePut(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var raw map[string]string
	if err := readJSON(w, r, &raw); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Tpl.UpdateUser(id, raw["name"], raw["content"]); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	rec, _ := s.Tpl.Get(id)
	writeOK(w, rec)
}

func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Tpl.Delete(id); err != nil {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleTemplateExport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	b, err := s.Tpl.Export(id)
	if err != nil {
		writeErr(w, 404, "not_found", "模板不存在")
		return
	}
	writeOK(w, json.RawMessage(b))
}

func (s *Server) handleTemplateImport(w http.ResponseWriter, r *http.Request) {
	var raw map[string]string
	if err := readJSON(w, r, &raw); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	id, err := s.Tpl.Import(raw["type"], raw["name"], raw["content"])
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	rec, _ := s.Tpl.Get(id)
	writeOK(w, rec)
}

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	items, err := s.Sites.List()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleSiteCreate(w http.ResponseWriter, r *http.Request) {
	var in site.In
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	st, err := s.Sites.Create(r.Context(), in)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, st)
}

func (s *Server) handleSiteGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	st, err := s.Sites.Get(id)
	if errors.Is(err, site.ErrNotFound) {
		writeErr(w, 404, "not_found", "站点不存在")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, st)
}

func (s *Server) handleSitePut(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in site.In
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	st, err := s.Sites.Update(r.Context(), id, in)
	if errors.Is(err, site.ErrDrift) || errors.Is(err, site.ErrRawMode) {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, st)
}

func (s *Server) handleSiteDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	purge := r.URL.Query().Get("purge_files") == "true"
	if err := s.Sites.Delete(r.Context(), id, purge); err != nil {
		if errors.Is(err, site.ErrPurge) {
			writeErr(w, 400, "invalid", err.Error())
			return
		}
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleSiteEnable(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Sites.SetEnabled(r.Context(), id, true); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleSiteDisable(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Sites.SetEnabled(r.Context(), id, false); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleSiteNginxGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	st, err := s.Sites.Get(id)
	if err != nil {
		writeErr(w, 404, "not_found", "站点不存在")
		return
	}
	body, err := s.Nginx.Read(st.SiteKey)
	if err != nil {
		writeErr(w, 404, "not_found", "配置文件不存在")
		return
	}
	writeOK(w, map[string]any{"content": body, "drifted": st.Drifted, "raw_mode": st.RawMode})
}

func (s *Server) handleSiteNginxPut(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Content string `json:"content"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	err := s.Sites.PutRaw(r.Context(), id, in.Content, in.Force)
	if errors.Is(err, site.ErrDrift) {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleSiteUseTpl(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Sites.UseTemplate(r.Context(), id); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleRedirects(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if r.Method == http.MethodGet {
		st, err := s.Sites.Get(id)
		if err != nil {
			writeErr(w, 404, "not_found", "站点不存在")
			return
		}
		writeOK(w, map[string]any{"items": st.Redirects})
		return
	}
	var in struct {
		Items []site.Redirect `json:"items"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Sites.PutRedirects(r.Context(), id, in.Items); err != nil {
		if errors.Is(err, site.ErrRawMode) {
			writeErr(w, 409, "conflict", err.Error())
			return
		}
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleSSLIssue(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.SSL.Issue(r.Context(), id); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleCerts(w http.ResponseWriter, r *http.Request) {
	items, err := s.SSL.List()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}
