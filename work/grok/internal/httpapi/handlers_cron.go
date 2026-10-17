package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"lnmp-panel/internal/cron"
)

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	if s.Cron == nil {
		writeErr(w, 500, "internal", "定时任务模块未初始化")
		return
	}
	items, err := s.Cron.List()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items, "drifted": s.Cron.Drifted(r.Context())})
}

func (s *Server) handleCronCreate(w http.ResponseWriter, r *http.Request) {
	if s.Cron == nil {
		writeErr(w, 500, "internal", "定时任务模块未初始化")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	en := true
	if in.Enabled != nil {
		en = *in.Enabled
	}
	j, err := s.Cron.Create(r.Context(), in.Name, in.Schedule, in.Command, en)
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, j)
}

func cronID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func writeCronErr(w http.ResponseWriter, err error) {
	if errors.Is(err, cron.ErrNotFound) {
		writeErr(w, 404, "not_found", err.Error())
		return
	}
	writeErr(w, 400, "invalid", err.Error())
}

func (s *Server) handleCronPut(w http.ResponseWriter, r *http.Request) {
	if s.Cron == nil {
		writeErr(w, 500, "internal", "定时任务模块未初始化")
		return
	}
	id, err := cronID(r)
	if err != nil {
		writeErr(w, 400, "invalid", "无效 ID")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	en := true
	if in.Enabled != nil {
		en = *in.Enabled
	}
	if err := s.Cron.Update(r.Context(), id, in.Name, in.Schedule, in.Command, en); err != nil {
		writeCronErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	if s.Cron == nil {
		writeErr(w, 500, "internal", "定时任务模块未初始化")
		return
	}
	id, err := cronID(r)
	if err != nil {
		writeErr(w, 400, "invalid", "无效 ID")
		return
	}
	if err := s.Cron.Delete(r.Context(), id); err != nil {
		writeCronErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleCronRun(w http.ResponseWriter, r *http.Request) {
	if s.Cron == nil {
		writeErr(w, 500, "internal", "定时任务模块未初始化")
		return
	}
	id, err := cronID(r)
	if err != nil {
		writeErr(w, 400, "invalid", "无效 ID")
		return
	}
	if err := s.Cron.RunNow(r.Context(), id); err != nil {
		writeCronErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleScheduler(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil {
		writeOK(w, map[string]any{"items": []any{}})
		return
	}
	writeOK(w, map[string]any{"items": s.Scheduler.List()})
}
