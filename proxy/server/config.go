package server

import (
	"gitlab.atcatw.org/atca/community-edition/trojan-go/config"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/proxy/client"
)

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(client.Config)
	})
}
