package httpapi

import (
	"encoding/json"
	"net/http"

	"lnmp-panel/internal/migrate"
)

func (s *Server) handleMigrateToken(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	out, err := s.Migrate.Token()
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleMigrateConnect(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	var in migrate.ConnectIn
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	out, err := s.Migrate.Connect(r.Context(), in)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleMigratePreflight(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	out, err := s.Migrate.PreflightPeer(r.Context(), 1<<30)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleMigrateStart(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	id, err := s.Migrate.Start(r.Context(), in.Mode)
	if err != nil {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	writeOK(w, map[string]any{"job_id": id})
}

func (s *Server) handleMigrateStatus(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	st, err := s.Migrate.Status()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, st)
}

func (s *Server) handleMigrateCancel(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	if err := s.Migrate.Cancel(r.Context()); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMigrateAccept(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	var in migrate.AcceptIn
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	out, err := s.Migrate.Accept(in)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleMigratePeerPreflight(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	var in struct {
		Token     string `json:"token"`
		NeedBytes int64  `json:"need_bytes"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	out, err := s.Migrate.PeerPreflight(migrate.PreflightIn{Token: in.Token, NeedBytes: in.NeedBytes})
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleMigratePeerApply(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	var in struct {
		Token    string          `json:"token"`
		Bundle   migrate.Bundle  `json:"bundle"`
		DumpPath string          `json:"dump_path"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	if err := dec.Decode(&in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Migrate.PeerApply(r.Context(), migrate.ApplyIn{Token: in.Token, Bundle: in.Bundle, DumpPath: in.DumpPath}); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMigratePeerCancel(w http.ResponseWriter, r *http.Request) {
	if s.Migrate == nil {
		writeErr(w, 500, "internal", "迁移模块未初始化")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Migrate.PeerCancel(in.Token); err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}
