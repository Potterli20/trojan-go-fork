package transport

import (
	"github.com/Potterli20/trojan-go-fork/config"
)

type Config struct {
	LocalHost       string                `json:"local_addr" yaml:"local-addr"`
	LocalPort       int                   `json:"local_port" yaml:"local-port"`
	RemoteHost      string                `json:"remote_addr" yaml:"remote-addr"`
	RemotePort      int                   `json:"remote_port" yaml:"remote-port"`
	TCP             TCPConfig             `json:"tcp" yaml:"tcp"`
	TransportPlugin TransportPluginConfig `json:"transport_plugin" yaml:"transport-plugin"`
}

type TCPConfig struct {
	FastOpen bool `json:"fast_open" yaml:"fast-open"`
}

type TransportPluginConfig struct {
	Enabled bool     `json:"enabled" yaml:"enabled"`
	Type    string   `json:"type" yaml:"type"`
	Command string   `json:"command" yaml:"command"`
	Option  string   `json:"option" yaml:"option"`
	Arg     []string `json:"arg" yaml:"arg"`
	Env     []string `json:"env" yaml:"env"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{
			TCP: TCPConfig{
				FastOpen: true,
			},
		}
	})
}
