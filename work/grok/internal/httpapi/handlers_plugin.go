package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeOK(w, map[string]any{"items": []any{}})
		return
	}
	writeOK(w, map[string]any{"items": s.Plugins.ListInfo()})
}

func (s *Server) handlePluginPut(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeErr(w, 404, "not_found", "插件框架未初始化")
		return
	}
	id := r.PathValue("id")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(w, r, &in); err != nil || in.Enabled == nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Plugins.SetEnabled(id, *in.Enabled); err != nil {
		if strings.Contains(err.Error(), "不存在") {
			writeErr(w, 404, "not_found", err.Error())
			return
		}
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handlePluginDispatch(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeErr(w, 404, "not_found", "插件未启用")
		return
	}
	id := r.PathValue("id")
	rel := r.PathValue("path")
	s.Plugins.Dispatch(w, r, id, rel)
}
