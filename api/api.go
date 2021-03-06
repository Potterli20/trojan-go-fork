package api

import (
	"context"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
)

type Handler func(ctx context.Context, auth statistic.Authenticator) error

var handlers = make(map[string]Handler)

func RegisterHandler(name string, handler Handler) {
	handlers[name] = handler
}

func RunService(ctx context.Context, name string, auth statistic.Authenticator) error {
	if h, ok := handlers[name]; ok {
		log.Debug("api handler found", name)
		return h(ctx, auth)
	}
	log.Debug("api handler not found", name)
	return nil
}
