package server

import (
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/proxy/client"
)

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(client.Config)
	})
}
