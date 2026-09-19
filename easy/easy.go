package easy

import (
	"encoding/json"
	"flag"
	"net"
	"strconv"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/option"
	"github.com/Potterli20/trojan-go-fork/proxy"
)

type easy struct {
	server   *bool
	client   *bool
	password *string
	local    *string
	remote   *string
	cert     *string
	key      *string
}

type Config struct {
	RunType    string   `json:"run_type"`
	LocalAddr  string   `json:"local_addr"`
	LocalPort  int      `json:"local_port"`
	RemoteAddr string   `json:"remote_addr"`
	RemotePort int      `json:"remote_port"`
	Password   []string `json:"password"`
	TLS        *TLS     `json:"ssl,omitempty"`
}

type TLS struct {
	SNI  string `json:"sni"`
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

func parseAddr(addr, defaultAddr, name string) (host string, port int, err error) {
	if addr == "" {
		if defaultAddr == "" {
			return "", 0, common.NewError("invalid " + name + " addr: empty address")
		}
		log.Warn(name, " addr is unspecified, using ", defaultAddr)
		addr = defaultAddr
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, common.NewError("invalid " + name + " addr format:" + addr).Base(err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func (o *easy) Name() string {
	return "easy"
}

func (o *easy) Handle() error {
	if !*o.server && !*o.client {
		return common.NewError("empty")
	}
	if *o.password == "" {
		log.Fatal("empty password is not allowed")
	}
	log.Info("easy mode enabled, trojan-go will NOT use the config file")

	var config Config
	if *o.client {
		localHost, localPort, err := parseAddr(*o.local, "127.0.0.1:1080", "client local")
		if err != nil {
			log.Fatal(err)
		}
		remoteHost, remotePort, err := parseAddr(*o.remote, "", "remote")
		if err != nil {
			log.Fatal(err)
		}
		config = Config{
			RunType:    "client",
			LocalAddr:  localHost,
			LocalPort:  localPort,
			RemoteAddr: remoteHost,
			RemotePort: remotePort,
			Password:   []string{*o.password},
		}
	} else {
		localHost, localPort, err := parseAddr(*o.local, "0.0.0.0:443", "server local")
		if err != nil {
			log.Fatal(err)
		}
		remoteHost, remotePort, err := parseAddr(*o.remote, "127.0.0.1:80", "remote")
		if err != nil {
			log.Fatal(err)
		}
		config = Config{
			RunType:    "server",
			LocalAddr:  localHost,
			LocalPort:  localPort,
			RemoteAddr: remoteHost,
			RemotePort: remotePort,
			Password:   []string{*o.password},
			TLS: &TLS{
				Cert: *o.cert,
				Key:  *o.key,
			},
		}
	}

	configJSON, err := json.Marshal(&config) //gosec:disable -- 密码字段是协议必需的配置项，日志输出已通过 sanitizeConfigJSON 脱敏
	if err != nil {
		log.Fatal(err)
	}
	log.Info("generated config:")
	log.Info(sanitizeConfigJSON(configJSON))
	p, err := proxy.NewProxyFromConfigData(configJSON, true)
	if err != nil {
		log.Fatal(err)
	}
	// RunAndClose 会把关闭阶段的错误一起带回来，避免端口没释放也以退出码 0 结束
	if err := p.RunAndClose(); err != nil {
		log.Fatal(err)
	}
	return nil
}

func (o *easy) Priority() int {
	return 50
}

func init() {
	option.RegisterHandler(&easy{
		server:   flag.Bool("server", false, "Run a trojan-go server"),
		client:   flag.Bool("client", false, "Run a trojan-go client"),
		password: flag.String("password", "", "Password for authentication"),
		remote:   flag.String("remote", "", "Remote address, e.g. 127.0.0.1:12345"),
		local:    flag.String("local", "", "Local address, e.g. 127.0.0.1:12345"),
		key:      flag.String("key", "server.key", "Key of the server"),
		cert:     flag.String("cert", "server.crt", "Certificates of the server"),
	})
}

// sanitizeConfigJSON 将配置 JSON 中的 password 字段脱敏后返回字符串，用于日志输出。
// 传给 proxy.NewProxyFromConfigData 的原始 data 不受影响。
func sanitizeConfigJSON(data []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "[config parse error, not displayed]"
	}
	if pw, ok := raw["password"]; ok {
		if pwSlice, ok := pw.([]any); ok {
			for i := range pwSlice {
				pwSlice[i] = "***"
			}
			raw["password"] = pwSlice
		}
	}
	redacted, err := json.Marshal(raw)
	if err != nil {
		return "[config marshal error, not displayed]"
	}
	return string(redacted)
}
