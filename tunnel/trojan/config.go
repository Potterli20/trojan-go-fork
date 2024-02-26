package trojan

import "gitlab.atcatw.org/atca/community-edition/trojan-go.git/config"

type Config struct {
	LocalHost        string      `json:"local_addr" yaml:"local-addr"`
	LocalPort        int         `json:"local_port" yaml:"local-port"`
	RemoteHost       string      `json:"remote_addr" yaml:"remote-addr"`
	RemotePort       int         `json:"remote_port" yaml:"remote-port"`
	DisableHTTPCheck bool        `json:"disable_http_check" yaml:"disable-http-check"`
	RecordCapacity   int         `json:"record_capacity" yaml:"record-capacity"`
	MySQL            MySQLConfig `json:"mysql" yaml:"mysql"`
	API              APIConfig   `json:"api" yaml:"api"`
}

type MySQLConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type APIConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{}
	})
}
