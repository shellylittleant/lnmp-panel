package httpapi

import (
	"errors"
	"net/http"

	"lnmp-panel/internal/php"
)

func (s *Server) handlePHPRepo(w http.ResponseWriter, r *http.Request) {
	if s.Stack == nil {
		writeErr(w, 500, "internal", "安装器未初始化")
		return
	}
	id, err := s.Stack.StartPHPRepo(r.Context())
	if err != nil {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	writeOK(w, map[string]any{"job_id": id})
}

func (s *Server) handleMySQLInstall(w http.ResponseWriter, r *http.Request) {
	if s.Stack == nil {
		writeErr(w, 500, "internal", "安装器未初始化")
		return
	}
	id, err := s.Stack.StartMySQL(r.Context())
	if err != nil {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	writeOK(w, map[string]any{"job_id": id})
}

func writePHPErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, php.ErrNotFound):
		writeErr(w, 404, "not_found", err.Error())
	case errors.Is(err, php.ErrDrift), errors.Is(err, php.ErrRawMode):
		writeErr(w, 409, "conflict", err.Error())
	case errors.Is(err, php.ErrPPA), errors.Is(err, php.ErrVersion), errors.Is(err, php.ErrUnavailable):
		writeErr(w, 400, "not_supported", err.Error())
	default:
		writeErr(w, 400, "invalid", err.Error())
	}
}

func (s *Server) handlePHPVersions(w http.ResponseWriter, r *http.Request) {
	if s.PHP == nil {
		writeErr(w, 500, "internal", "PHP 模块未初始化")
		return
	}
	items, err := s.PHP.List(r.Context())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items, "ppa": php.PPAPresent()})
}

func (s *Server) handlePHPInstall(w http.ResponseWriter, r *http.Request) {
	ver := r.PathValue("ver")
	id, err := s.PHP.StartInstall(r.Context(), ver)
	if err != nil {
		if errors.Is(err, php.ErrPPA) || errors.Is(err, php.ErrVersion) || errors.Is(err, php.ErrUnavailable) {
			writePHPErr(w, err)
			return
		}
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	writeOK(w, map[string]any{"job_id": id})
}

func (s *Server) handlePHPExtGet(w http.ResponseWriter, r *http.Request) {
	ver := r.PathValue("ver")
	items, err := s.PHP.List(r.Context())
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	for _, it := range items {
		if it.Version == ver {
			writeOK(w, map[string]any{"items": it.Extensions})
			return
		}
	}
	writeErr(w, 404, "not_found", php.ErrNotFound.Error())
}

func (s *Server) handlePHPExtAdd(w http.ResponseWriter, r *http.Request) {
	ver := r.PathValue("ver")
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.PHP.InstallExt(r.Context(), ver, in.Name); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPExtDel(w http.ResponseWriter, r *http.Request) {
	if err := s.PHP.RemoveExt(r.Context(), r.PathValue("ver"), r.PathValue("name")); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPIniGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.PHP.GetINI(r.PathValue("ver"))
	if err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, v)
}

func (s *Server) handlePHPIniPut(w http.ResponseWriter, r *http.Request) {
	var in php.INIVars
	var wrap struct {
		php.INIVars
		Force bool `json:"force"`
	}
	if err := readJSON(w, r, &wrap); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	in = wrap.INIVars
	if err := s.PHP.PutINI(r.Context(), r.PathValue("ver"), in, wrap.Force); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPIniRaw(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.PHP.PutINIRaw(r.Context(), r.PathValue("ver"), in.Content, in.Force); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPIniUseTpl(w http.ResponseWriter, r *http.Request) {
	if err := s.PHP.UseINITemplate(r.Context(), r.PathValue("ver")); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPPoolGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.PHP.GetPool(r.PathValue("ver"))
	if err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, v)
}

func (s *Server) handlePHPPoolPut(w http.ResponseWriter, r *http.Request) {
	var wrap struct {
		php.PoolVars
		Force bool `json:"force"`
	}
	if err := readJSON(w, r, &wrap); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.PHP.PutPool(r.Context(), r.PathValue("ver"), wrap.PoolVars, wrap.Force); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPPoolRaw(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
		Force   bool   `json:"force"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.PHP.PutPoolRaw(r.Context(), r.PathValue("ver"), in.Content, in.Force); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePHPPoolUseTpl(w http.ResponseWriter, r *http.Request) {
	if err := s.PHP.UsePoolTemplate(r.Context(), r.PathValue("ver")); err != nil {
		writePHPErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}
