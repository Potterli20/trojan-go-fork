package redirector

import "github.com/Potterli20/trojan-go-fork/config"

// Name 是重定向（诱饵/回落）这一层在配置注册表里的名字。
//
// 它不是 tunnel：trojan / shadowsocks / tls / websocket 四个入站都调用本包，
// 开关放在这里是一处生效、四处一致，不必在四个模块的配置结构体里各加一个键
// （各加一份反而会踩「同一份文档喂给所有模块、同一路径必须同类型」这条约束）。
const Name = "REDIRECTION"

type Config struct {
	// RedirectClientIP 决定是否把客户端地址写进转发给诱饵服务器的请求头
	// （X-Forwarded-For / X-Real-IP）。默认 false —— 保持旧行为：重定向流量
	// 原样中继，一个字节都不改。
	RedirectClientIP bool `json:"redirect_client_ip" yaml:"redirect-client-ip"`
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		return &Config{}
	})
}
