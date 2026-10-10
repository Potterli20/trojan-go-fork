package quic

import (
	"github.com/Potterli20/trojan-go-fork/config"
)

const Name = "QUIC"

type Config struct {
	RemoteHost string     `json:"remote_addr" yaml:"remote-addr"`
	RemotePort int        `json:"remote_port" yaml:"remote-port"`
	QUIC       QUICConfig `json:"quic" yaml:"quic"`
}

type QUICConfig struct {
	Enabled             bool   `json:"enabled" yaml:"enabled"`
	MaxIdleTimeout      int    `json:"max_idle_timeout" yaml:"max-idle-timeout"`
	MaxIncomingStreams  int    `json:"max_incoming_streams" yaml:"max-incoming-streams"`
	InitialStreamWindow int    `json:"initial_stream_window" yaml:"initial-stream-window"`
	InitialConnWindow   int    `json:"initial_conn_window" yaml:"initial-conn-window"`
	ALPN                string `json:"alpn" yaml:"alpn"`
	Insecure            bool   `json:"insecure" yaml:"insecure"`
	Congestion          string `json:"congestion" yaml:"congestion"`
	BrutalUp            uint64 `json:"brutal_up" yaml:"brutal-up"`
	BrutalDown          uint64 `json:"brutal_down" yaml:"brutal-down"`
}

// newDefaultConfig 是 QUIC 配置的默认值唯一来源：既供 config 注册使用，也被测试
// 直接调用，避免"测试里的默认值"和"生产环境的默认值"变成两份。
func newDefaultConfig() *Config {
	return &Config{
		QUIC: QUICConfig{
			Enabled:            false,
			MaxIdleTimeout:     30,
			MaxIncomingStreams: 100,
			// 0 是 quic-go 的"用库内默认"哨兵，两个初始窗口都是 512KB
			// （interface.go:125-139）。这里原先写的是 65535，而这两个键当时根本没接进
			// quic.Config，所以没人察觉：一旦接线，就等于在一次"让配置生效"的改动里
			// 把所有人的默认流控窗口缩到 1/8。保持 0 把决定权交给库，用户显式设置即可生效。
			InitialStreamWindow: 0,
			InitialConnWindow:   0,
			ALPN:                "hq-29",
			Insecure:            false,
			Congestion:          "bbr",
			BrutalUp:            0,
			BrutalDown:          0,
		},
	}
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return newDefaultConfig()
	})
}
