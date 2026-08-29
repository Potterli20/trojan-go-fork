package proxy

import "github.com/Potterli20/trojan-go-fork/config"

type Config struct {
	RunType          string `json:"run_type" yaml:"run-type"`
	LogLevel         int    `json:"log_level" yaml:"log-level"`
	LogFile          string `json:"log_file" yaml:"log-file"`
	RelayBufferSize  int    `json:"relay_buffer_size" yaml:"relay_buffer_size"`
	RelayBufferCount int    `json:"relay_buffer_count" yaml:"relay_buffer_count"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{
			LogLevel:         1,
			RelayBufferSize:  8 * 1024,
			RelayBufferCount: 1024,
		}
	})
}
