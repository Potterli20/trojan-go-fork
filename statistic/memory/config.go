package memory

import (
	"gitlab.atcatw.org/atca/community-edition/trojan-go/config"
)

type Config struct {
	Passwords    []string `json:"password" yaml:"password"`
	Sqlite       string   `json:"sqlite" yaml:"sqlite"`
	MaxIPPerUser int      `json:"MaxIPPerUser" yaml:"MaxIPPerUser"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{}
	})
}
