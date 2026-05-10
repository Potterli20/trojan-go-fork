package quic

import (
	"github.com/Potterli20/trojan-go-fork/config"
)

const Name = "QUIC"

type Config struct {
	RemoteHost string    `json:"remote_addr" yaml:"remote-addr"`
	RemotePort int       `json:"remote_port" yaml:"remote-port"`
	QUIC       QUICConfig `json:"quic" yaml:"quic"`
}

type QUICConfig struct {
	Enabled            bool   `json:"enabled" yaml:"enabled"`
	MaxIdleTimeout     int    `json:"max_idle_timeout" yaml:"max-idle-timeout"`
	MaxIncomingStreams int    `json:"max_incoming_streams" yaml:"max-incoming-streams"`
	InitialStreamWindow int    `json:"initial_stream_window" yaml:"initial-stream-window"`
	InitialConnWindow   int    `json:"initial_conn_window" yaml:"initial-conn-window"`
	ALPN               string `json:"alpn" yaml:"alpn"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{
			QUIC: QUICConfig{
				Enabled:            false,
				MaxIdleTimeout:     30,
				MaxIncomingStreams: 100,
				InitialStreamWindow: 65535,
				InitialConnWindow:   65535,
				ALPN:               "hq-29",
			},
		}
	})
}