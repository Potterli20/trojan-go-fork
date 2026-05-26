package option

import (
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

	var maxHandler Handler
	for _, h := range handlers {
		if maxHandler == nil || maxHandler.Priority() < h.Priority() {
			maxHandler = h
		}
	}
	if maxHandler == nil {
		return nil, common.NewError("no option handler available")
	}
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
