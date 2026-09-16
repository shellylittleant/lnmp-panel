package plugin

import "sync"

type Plugin interface {
	ID() string
	Name() string
	Version() string
}

var (
	mu  sync.Mutex
	all []Plugin
)

func Register(p Plugin) {
	mu.Lock()
	defer mu.Unlock()
	all = append(all, p)
}

func List() []Plugin {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Plugin, len(all))
	copy(out, all)
	return out
}
