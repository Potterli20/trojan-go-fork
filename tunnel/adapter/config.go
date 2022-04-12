package adapter

import "github.com/Potterli20/trojan-go-fork/config"

type Config struct {
	LocalHost string `json:"local_addr" yaml:"local-addr"`
	LocalPort int    `json:"local_port" yaml:"local-port"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(Config)
	})
}
