package quic

import (
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
)

const Name = "QUIC"

type Config struct {
	RemoteHost string     `json:"remote_addr" yaml:"remote-addr"`
	RemotePort int        `json:"remote_port" yaml:"remote-port"`
	QUIC       QUICConfig `json:"quic" yaml:"quic"`
}

type QUICConfig struct {
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
			MaxIdleTimeout:     30,
			MaxIncomingStreams: 100,
			// 0 是 quic-go 的"用库内默认"哨兵：单流 2MB、连接 3MB
			// （internal/protocol/params.go 的 DefaultInitialMaxStreamData 与 1.5 倍）。
			// 这里原先写的是 65535，而这两个键当时根本没接进
			// quic.Config，所以没人察觉：一旦接线，就等于在一次"让配置生效"的改动里
			// 把所有人的默认流控窗口缩到 1/32。保持 0 把决定权交给库，用户显式设置即可生效。
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

// ValidateQuicParams 拒绝会被无符号/时长转换毒化的负值配置。
// 这几个字段都是 int，却在建栈时直接 uint64(cfg.InitialStreamWindow) 喂给
// quic.Config，或 time.Duration(cfg.MaxIdleTimeout) 变成负 duration：
//   - 窗口 -1 → uint64 约 1.8e19 字节的流控窗口，对端可据此灌入海量数据；
//   - MaxIncomingStreams -1 → int64 负值；
//   - MaxIdleTimeout -1 → 负 duration，且 KeepAlivePeriod=idle/2 也变负。
//
// 0 是合法语义（窗口交给 quic-go 库默认、idle=0 关保活），所以只拒绝负值，
// 不擅自收紧 0。BrutalUp/Down 本身是 uint64，不在此检查。
func ValidateQuicParams(cfg QUICConfig) error {
	if cfg.MaxIdleTimeout < 0 {
		return common.NewError("quic: max_idle_timeout must not be negative")
	}
	if cfg.MaxIncomingStreams < 0 {
		return common.NewError("quic: max_incoming_streams must not be negative")
	}
	if cfg.InitialStreamWindow < 0 {
		return common.NewError("quic: initial_stream_window must not be negative")
	}
	if cfg.InitialConnWindow < 0 {
		return common.NewError("quic: initial_conn_window must not be negative")
	}
	return nil
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return newDefaultConfig()
	})
}
