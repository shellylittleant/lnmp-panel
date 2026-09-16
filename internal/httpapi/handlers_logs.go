package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"lnmp-panel/internal/logview"
)

func (s *Server) handleLogSources(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, 500, "internal", "日志模块未初始化")
		return
	}
	writeOK(w, map[string]any{"items": s.Logs.Sources()})
}

func (s *Server) handleLogQuery(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, 500, "internal", "日志模块未初始化")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := s.Logs.Query(r.Context(), logview.QueryIn{
		Source: q.Get("source"), Site: q.Get("site"), Q: q.Get("q"),
		Level: q.Get("level"), From: q.Get("from"), To: q.Get("to"), Limit: limit,
	})
	if errors.Is(err, logview.ErrUnknownSource) {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}

func (s *Server) handleLogFollow(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, 500, "internal", "日志模块未初始化")
		return
	}
	q := r.URL.Query()
	src := q.Get("source")
	site := q.Get("site")
	if _, ok := s.Logs.ResolveSource(src, site); !ok {
		writeErr(w, 400, "invalid", logview.ErrUnknownSource.Error())
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	_ = s.Logs.Follow(ctx, src, site, func(ln logview.Line) error {
		if ln.Level == "ping" {
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return err
			}
		} else {
			b, _ := json.Marshal(ln)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return err
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
}

func (s *Server) handleLogStats(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, 500, "internal", "日志模块未初始化")
		return
	}
	q := r.URL.Query()
	out, err := s.Logs.Stats(q.Get("kind"), q.Get("site"), q.Get("from"), q.Get("to"))
	if err != nil {
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, out)
}

func (s *Server) handleLogRotateGet(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, 500, "internal", "日志模块未初始化")
		return
	}
	f, err := s.Logs.GetRotate()
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, f)
}

func (s *Server) handleLogRotatePut(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		writeErr(w, 500, "internal", "日志模块未初始化")
		return
	}
	var in logview.RotateForm
	if err := readJSON(w, r, &in); err != nil {
		writeErr(w, 400, "invalid", "请求格式错误")
		return
	}
	if err := s.Logs.PutRotate(r.Context(), in); err != nil {
		if errors.Is(err, logview.ErrDrift) {
			writeErr(w, 409, "conflict", err.Error())
			return
		}
		writeErr(w, 400, "invalid", err.Error())
		return
	}
	writeOK(w, map[string]any{})
}

func (s *Server) handleMetricsCurrent(w http.ResponseWriter, r *http.Request) {
	if s.Monitor == nil {
		writeErr(w, 500, "internal", "监控模块未初始化")
		return
	}
	sm, err := s.Monitor.Latest()
	if err != nil {
		sm, err = s.Monitor.Snapshot()
		if err != nil {
			writeErr(w, 500, "internal", err.Error())
			return
		}
	}
	writeOK(w, sm)
}

func (s *Server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	if s.Monitor == nil {
		writeErr(w, 500, "internal", "监控模块未初始化")
		return
	}
	q := r.URL.Query()
	to := q.Get("to")
	from := q.Get("from")
	if to == "" {
		to = time.Now().UTC().Format(time.RFC3339)
	}
	if from == "" {
		from = time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	}
	step := q.Get("step")
	if step == "" {
		step = "raw"
	}
	items, err := s.Monitor.History(from, to, step)
	if err != nil {
		writeErr(w, 500, "internal", err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items})
}
