package plugin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"lnmp-panel/internal/store"
)

type Info struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	Pages   []Page `json:"pages"`
	Error   string `json:"error,omitempty"`
}

type Registry struct {
	db   *store.DB
	host Context

	mu     sync.Mutex
	errors map[string]string
	live   map[string]bool
}

func New(db *store.DB, host Context) *Registry {
	return &Registry{
		db:     db,
		host:   host,
		errors: map[string]string{},
		live:   map[string]bool{},
	}
}

func (r *Registry) Host() Context { return r.host }

func (r *Registry) Sync() error {
	if r.db == nil {
		return nil
	}
	for _, p := range List() {
		_, err := r.db.SQL.Exec(`INSERT OR IGNORE INTO plugins(id, enabled, config) VALUES (?,1,'{}')`, p.ID())
		if err != nil {
			return err
		}
	}
	return r.startEnabled()
}

func (r *Registry) startEnabled() error {
	for _, p := range List() {
		if !r.Enabled(p.ID()) {
			continue
		}
		if err := r.start(p); err != nil {
			log.Printf("插件 %s 启动：%v", p.ID(), err)
			r.setErr(p.ID(), err.Error())
		}
	}
	return nil
}

func (r *Registry) ListInfo() []Info {
	var out []Info
	for _, p := range List() {
		info := Info{
			ID: p.ID(), Name: p.Name(), Version: p.Version(),
			Enabled: r.Enabled(p.ID()), Pages: p.Pages(), Error: r.getErr(p.ID()),
		}
		if !info.Enabled {
			info.Pages = nil
		}
		out = append(out, info)
	}
	if out == nil {
		out = []Info{}
	}
	return out
}

func (r *Registry) Enabled(id string) bool {
	if r.db == nil {
		return false
	}
	var n int
	_ = r.db.SQL.QueryRow(`SELECT enabled FROM plugins WHERE id=?`, id).Scan(&n)
	return n == 1
}

func (r *Registry) SetEnabled(id string, on bool) error {
	var p Plugin
	for _, x := range List() {
		if x.ID() == id {
			p = x
			break
		}
	}
	if p == nil {
		return fmt.Errorf("插件不存在")
	}
	v := 0
	if on {
		v = 1
	}
	if _, err := r.db.SQL.Exec(`INSERT INTO plugins(id, enabled, config) VALUES (?,?, '{}')
		ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled`, id, v); err != nil {
		return err
	}
	if on {
		return r.start(p)
	}
	return r.stop(p)
}

func (r *Registry) start(p Plugin) error {
	r.mu.Lock()
	if r.live[p.ID()] {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	if err := p.OnStart(r.host); err != nil {
		r.setErr(p.ID(), err.Error())
		return err
	}
	r.mu.Lock()
	r.live[p.ID()] = true
	delete(r.errors, p.ID())
	r.mu.Unlock()
	return nil
}

func (r *Registry) stop(p Plugin) error {
	if err := p.OnStop(r.host); err != nil {
		r.setErr(p.ID(), err.Error())
	}
	r.mu.Lock()
	delete(r.live, p.ID())
	r.mu.Unlock()
	return nil
}

func (r *Registry) Dispatch(w http.ResponseWriter, req *http.Request, id, rel string) {
	if id == "" || find(id) == nil {
		pluginNotFound(w, "插件不存在")
		return
	}
	if !r.Enabled(id) {
		pluginNotFound(w, "插件未启用")
		return
	}
	rel = strings.Trim(rel, "/")
	p := find(id)
	for _, rt := range p.Routes() {
		if !strings.EqualFold(rt.Method, req.Method) {
			continue
		}
		if strings.Trim(rt.Path, "/") != rel {
			continue
		}
		if rt.Handle == nil {
			pluginNotFound(w, "未实现")
			return
		}
		rt.Handle(w, req)
		return
	}
	pluginNotFound(w, "接口不存在")
}

func find(id string) Plugin {
	for _, x := range List() {
		if x.ID() == id {
			return x
		}
	}
	return nil
}

func pluginNotFound(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "not_found", "message": msg}})
}

func (r *Registry) PageState(path string) (isPlugin bool, enabled bool) {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	for _, p := range List() {
		for _, pg := range p.Pages() {
			pp := strings.TrimSuffix(pg.Path, "/")
			if pp == path {
				return true, r.Enabled(p.ID())
			}
		}
	}
	return false, false
}

func (r *Registry) RunHook(name string, ev Event) []Result {
	var out []Result
	for _, p := range List() {
		if !r.Enabled(p.ID()) {
			continue
		}
		for _, h := range p.Hooks() {
			if h == nil || h.Name() != name {
				continue
			}
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						msg := fmt.Sprintf("%v", rec)
						r.setErr(p.ID(), msg)
						out = append(out, Result{Err: msg})
					}
				}()
				res, err := h.Handle(r.host, ev)
				if err != nil {
					res.Err = err.Error()
					r.setErr(p.ID(), err.Error())
				}
				out = append(out, res)
			}()
		}
	}
	return out
}

func (r *Registry) setErr(id, msg string) {
	r.mu.Lock()
	r.errors[id] = msg
	r.mu.Unlock()
}

func (r *Registry) getErr(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.errors[id]
}
