package option

import (
	"cmp"
	"maps"
	"slices"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
)

type Handler interface {
	Name() string
	Handle() error
	Priority() int
}

var (
	handlers = make(map[string]Handler)
	mu       sync.RWMutex
)

func RegisterHandler(h Handler) {
	mu.Lock()
	defer mu.Unlock()
	handlers[h.Name()] = h
}

func PopOptionHandler() (Handler, error) {
	mu.Lock()
	defer mu.Unlock()

	candidates := slices.Collect(maps.Values(handlers))
	if len(candidates) == 0 {
		return nil, common.NewError("no option handler available")
	}
	maxHandler := slices.MaxFunc(candidates, func(a, b Handler) int {
		return cmp.Compare(a.Priority(), b.Priority())
	})
	delete(handlers, maxHandler.Name())
	return maxHandler, nil
}

func GetHandler(name string) (Handler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	h, ok := handlers[name]
	return h, ok
}

func HandlerCount() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(handlers)
}
