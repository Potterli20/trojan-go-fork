package api

import (
	"context"
	"sync"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
)

type Handler func(ctx context.Context, auth statistic.Authenticator) error

var (
	handlers = make(map[string]Handler)
	mu       sync.RWMutex
)

func RegisterHandler(name string, handler Handler) {
	mu.Lock()
	defer mu.Unlock()
	handlers[name] = handler
}

func RunService(ctx context.Context, name string, auth statistic.Authenticator) error {
	mu.RLock()
	h, ok := handlers[name]
	mu.RUnlock()
	if ok {
		log.Debug("api handler found", name)
		return h(ctx, auth)
	}
	log.Debug("api handler not found", name)
	return nil
}
