package url

import (
	"encoding/json"
	"flag"
	"net"
	"strconv"
	"strings"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/option"
	"github.com/Potterli20/trojan-go-fork/proxy"
)

const Name = "URL"

type Websocket struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Path    string `json:"path"`
}

type TLS struct {
	SNI string `json:"sni"`
}

type Shadowsocks struct {
	Enabled  bool   `json:"enabled"`
	Method   string `json:"method"`
	Password string `json:"password"`
}

type Mux struct {
	Enabled bool `json:"enabled"`
}

type API struct {
	Enabled bool   `json:"enabled"`
	APIHost string `json:"api_addr"`
	APIPort int    `json:"api_port"`
}

type UrlConfig struct {
	RunType     string   `json:"run_type"`
	LocalAddr   string   `json:"local_addr"`
	LocalPort   int      `json:"local_port"`
	RemoteAddr  string   `json:"remote_addr"`
	RemotePort  int      `json:"remote_port"`
	Password    []string `json:"password"`
	Websocket   `json:"websocket"`
	Shadowsocks `json:"shadowsocks"`
	TLS         `json:"ssl"`
	Mux         `json:"mux"`
	API         `json:"api"`
}

type URLOption struct {
	urlStr  *string
	options *string
}

func (u *URLOption) Name() string {
	return Name
}

func parseOptions(optStr string) (map[string]string, error) {
	result := make(map[string]string)
	if optStr == "" {
		return result, nil
	}
	pairs := strings.SplitSeq(optStr, ";")
	for pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			return nil, common.NewError("invalid option format: " + pair + ". expected key=value")
		}
		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return result, nil
}

func parseHostPort(hostPort string, defaultHost string, defaultPort int) (string, int, error) {
	if hostPort == "" {
		return defaultHost, defaultPort, nil
	}
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", 0, common.NewError("invalid host:port: " + hostPort).Base(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, common.NewError("invalid port: " + portStr).Base(err)
	}
	if host == "" {
		host = defaultHost
	}
	return host, port, nil
}

func (u *URLOption) Handle() error {
	if u.urlStr == nil || *u.urlStr == "" {
		return common.NewError("url is empty")
	}

	info, err := NewShareInfoFromURL(*u.urlStr)
	if err != nil {
		return common.NewError("failed to parse URL").Base(err)
	}

	opts, err := parseOptions(*u.options)
	if err != nil {
		return err
	}

	listenHost := "127.0.0.1"
	listenPort := 1080
	if listenVal, ok := opts["listen"]; ok {
		listenHost, listenPort, err = parseHostPort(listenVal, listenHost, listenPort)
		if err != nil {
			return err
		}
	}

	muxEnabled := false
	if muxVal, ok := opts["mux"]; ok {
		muxEnabled, err = strconv.ParseBool(muxVal)
		if err != nil {
			return common.NewError("invalid mux value: " + muxVal).Base(err)
		}
	}

	apiEnabled := false
	apiHost := "127.0.0.1"
	apiPort := 10000
	if apiVal, ok := opts["api"]; ok {
		apiEnabled = true
		apiHost, apiPort, err = parseHostPort(apiVal, apiHost, apiPort)
		if err != nil {
			return err
		}
	}

	wsEnabled := info.Type == ShareInfoTypeWebSocket

	var ssEnabled bool
	var ssMethod, ssPassword string
	if ssConfig, ok := strings.CutPrefix(info.Encryption, "ss;"); ok {
		var found bool
		ssMethod, ssPassword, found = strings.Cut(ssConfig, ":")
		if !found {
			return common.NewError("invalid shadowsocks config: " + info.Encryption)
		}
		ssEnabled = true
	}

	config := UrlConfig{
		RunType:    "client",
		LocalAddr:  listenHost,
		LocalPort:  listenPort,
		RemoteAddr: info.TrojanHost,
		RemotePort: int(info.Port),
		Password:   []string{info.TrojanPassword},
		SNI:        info.SNI,
		Websocket: Websocket{
			Enabled: wsEnabled,
			Path:    info.Path,
			Host:    info.Host,
		},
		Mux: Mux{
			Enabled: muxEnabled,
		},
		Shadowsocks: Shadowsocks{
			Enabled:  ssEnabled,
			Password: ssPassword,
			Method:   ssMethod,
		},
		API: API{
			Enabled: apiEnabled,
			APIHost: apiHost,
			APIPort: apiPort,
		},
	}

	data, err := json.Marshal(&config) //gosec:disable -- 密码字段是协议必需的配置项，日志输出已通过 sanitizeConfigJSON 脱敏
	if err != nil {
		return common.NewError("failed to marshal config").Base(err)
	}
	log.Debug(sanitizeConfigJSON(data))

	client, err := proxy.NewProxyFromConfigData(data, true)
	if err != nil {
		return common.NewError("failed to create proxy").Base(err)
	}
	defer client.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	return client.Run()
}

func (u *URLOption) Priority() int {
	return 10
}

func init() {
	option.RegisterHandler(&URLOption{
		urlStr:  flag.String("url", "", "Setup trojan-go client with a URL link"),
		options: flag.String("url-option", "mux=true;listen=127.0.0.1:1080", "URL mode options (key=value pairs separated by semicolon)"),
	})
}

// sanitizeConfigJSON 将配置 JSON 中的敏感字段（password）脱敏后返回字符串，用于日志输出。
// 传给 proxy.NewProxyFromConfigData 的原始 data 不受影响。
func sanitizeConfigJSON(data []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "[config parse error, not displayed]"
	}
	// 脱敏顶层 password 字段（[]string 类型）
	if pw, ok := raw["password"]; ok {
		if pwSlice, ok := pw.([]any); ok {
			for i := range pwSlice {
				pwSlice[i] = "***"
			}
			raw["password"] = pwSlice
		}
	}
	// 脱敏 shadowsocks.password 字段（string 类型）
	if ss, ok := raw["shadowsocks"].(map[string]any); ok {
		if _, ok := ss["password"]; ok {
			ss["password"] = "***"
		}
	}
	redacted, err := json.Marshal(raw)
	if err != nil {
		return "[config marshal error, not displayed]"
	}
	return string(redacted)
}
