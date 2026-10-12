package mux

import (
	"time"

	"github.com/Potterli20/trojan-go-fork/config"
)

// idleDefaultTimeout 是 mux.idle-timeout 缺省或非正数时使用的时限。
// 配置创建器和 cleanLoop 的兜底必须共用这一个值，否则"没配"和"配错"会走出
// 两种行为（旧代码就是：间隔兜底 10s，回收判据仍用原始 0 值比较）。
const idleDefaultTimeout = 30 * time.Second

type MuxConfig struct {
	Enabled     bool `json:"enabled" yaml:"enabled"`
	IdleTimeout int  `json:"idle_timeout" yaml:"idle-timeout"`
	Concurrency int  `json:"concurrency" yaml:"concurrency"`
}

type Config struct {
	Mux MuxConfig `json:"mux" yaml:"mux"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{
			Mux: MuxConfig{
				Enabled:     false,
				IdleTimeout: int(idleDefaultTimeout / time.Second),
				Concurrency: 8,
			},
		}
	})
}
