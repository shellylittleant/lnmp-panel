package plugin

import (
	"path/filepath"

	"lnmp-panel/internal/store"
)

func openTestDB(dir string) (*store.DB, error) {
	return store.Open(filepath.Join(dir, "p.db"))
}

type stubPlug struct {
	id      string
	started int
	stopped int
	page    string
}

func (s *stubPlug) ID() string      { return s.id }
func (s *stubPlug) Name() string    { return "测试" }
func (s *stubPlug) Version() string { return "0" }
func (s *stubPlug) Hooks() []Hook   { return nil }
func (s *stubPlug) Routes() []Route {
	return []Route{{Method: "GET", Path: "ping", Help: "ping", Handle: nil}}
}
func (s *stubPlug) Pages() []Page {
	return []Page{{MenuTitle: "测", Path: s.page}}
}
func (s *stubPlug) OnStart(Context) error { s.started++; return nil }
func (s *stubPlug) OnStop(Context) error  { s.stopped++; return nil }

type panicPlug struct{}

func (p *panicPlug) ID() string      { return "panicp" }
func (p *panicPlug) Name() string    { return "恐慌" }
func (p *panicPlug) Version() string { return "0" }
func (p *panicPlug) Hooks() []Hook {
	return []Hook{HookFunc{N: "menu.register", F: func(Context, Event) (Result, error) {
		panic("boom")
	}}}
}
func (p *panicPlug) Routes() []Route       { return nil }
func (p *panicPlug) Pages() []Page         { return nil }
func (p *panicPlug) OnStart(Context) error { return nil }
func (p *panicPlug) OnStop(Context) error  { return nil }
