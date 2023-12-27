package server

import (
	"github.com/Potterli20/trojan-go/config"
	"github.com/Potterli20/trojan-go/proxy/client"
)

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(client.Config)
	})
}
