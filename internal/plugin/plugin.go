package plugin

import (
	"net/http"
	"sync"
)

// Plugin 编译期插件。阶段 8 冻结。
type Plugin interface {
	ID() string
	Name() string
	Version() string
	Hooks() []Hook
	Routes() []Route
	Pages() []Page
	OnStart(ctx Context) error
	OnStop(ctx Context) error
}

type Hook interface {
	Name() string
	Handle(ctx Context, ev Event) (Result, error)
}

type Route struct {
	Method string
	Path   string // 相对 /api/v1/plugin/<id>/
	Help   string
	Handle http.HandlerFunc
}

type Page struct {
	MenuParent string     `json:"menu_parent"`
	MenuTitle  string     `json:"menu_title"`
	Path       string     `json:"path"`
	Schema     FormSchema `json:"schema"`
	StaticDir  string     `json:"static_dir,omitempty"`
}

type FormSchema struct {
	Title   string   `json:"title"`
	Fields  []Field  `json:"fields"`
	Actions []Action `json:"actions"`
}

type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type Action struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

type Event struct {
	Name string
	Data any
}

type Result struct {
	Data any
	Err  string
}

type Context interface {
	WWW() string
	DataDir() string
	SecretsDir() string
	Setting(key string) string
	SetSetting(key, val string) error
	Config(pluginID string) string
	SetConfig(pluginID, json string) error
}

type HookFunc struct {
	N string
	F func(Context, Event) (Result, error)
}

func (h HookFunc) Name() string { return h.N }

func (h HookFunc) Handle(ctx Context, ev Event) (Result, error) {
	if h.F == nil {
		return Result{}, nil
	}
	return h.F(ctx, ev)
}

var (
	mu  sync.Mutex
	all []Plugin
)

func Register(p Plugin) {
	if p == nil || p.ID() == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, x := range all {
		if x.ID() == p.ID() {
			return
		}
	}
	all = append(all, p)
}

func List() []Plugin {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Plugin, len(all))
	copy(out, all)
	return out
}

func ResetForTest() {
	mu.Lock()
	all = nil
	mu.Unlock()
}
