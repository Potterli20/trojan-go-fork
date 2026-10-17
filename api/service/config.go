package service

import "github.com/Potterli20/trojan-go-fork/config"

const Name = "API_SERVICE"

type SSLConfig struct {
	Enabled        bool     `json:"enabled" yaml:"enabled"`
	CertPath       string   `json:"cert" yaml:"cert"`
	KeyPath        string   `json:"key" yaml:"key"`
	VerifyClient   bool     `json:"verify_client" yaml:"verify-client"`
	ClientCertPath []string `json:"client_cert" yaml:"client-cert"`
}

type APIConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	APIHost string `json:"api_addr" yaml:"api-addr"`
	APIPort int    `json:"api_port" yaml:"api-port"`
	// Token 是共享密钥：非空时每个 RPC 都必须带 `authorization: Bearer <token>`，
	// 由 newAPIServer 挂上的拦截器校验（常量时间比较）。内置命令行客户端用 `-api-token` 传。
	Token string `json:"token" yaml:"token"`
	// AllowInsecure 是失败关闭策略的显式逃生阀：监听非回环地址又没有任何凭据时，
	// 默认**拒绝启动**；写了这一行才放行（只 Warn）。刻意不给默认放行——静默裸开
	// 的 API 谁都能增删用户、尾随流量记录。
	AllowInsecure bool      `json:"allow_insecure" yaml:"allow-insecure"`
	SSL           SSLConfig `json:"ssl" yaml:"ssl"`
}

type Config struct {
	API APIConfig `json:"api" yaml:"api"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return new(Config)
	})
}
