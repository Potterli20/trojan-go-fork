package api

import (
	"context"

	"gitlab.atcatw.org/atca/community-edition/trojan-go/log"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/statistic"
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
