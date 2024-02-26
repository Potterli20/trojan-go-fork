package server

import (
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/config"
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/proxy/client"
)

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(client.Config)
	})
}
