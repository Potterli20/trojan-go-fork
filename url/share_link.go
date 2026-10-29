package url

import (
	"errors"
	"fmt"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	ShareInfoTypeOriginal  = "original"
	ShareInfoTypeWebSocket = "ws"
)

var validTypes = map[string]struct{}{
	ShareInfoTypeOriginal:  {},
	ShareInfoTypeWebSocket: {},
}

var validEncryptionProviders = map[string]struct{}{
	"ss":   {},
	"none": {},
}

var validSSEncryptionMap = map[string]struct{}{
	"aes-128-gcm":            {},
	"aes-256-gcm":            {},
	"chacha20-ietf-poly1305": {},
}

type ShareInfo struct {
	TrojanHost     string // 节点 IP / 域名
	Port           uint16 // 节点端口
	TrojanPassword string // Trojan 密码

	SNI  string // SNI
	Type string // 类型
	Host string // HTTP Host Header

	Path       string // WebSocket / H2 Path
	Encryption string // 额外加密
	Plugin     string // 插件设定

	Description string // 节点说明
}

// urlUserinfoRe 命中 scheme://<userinfo>@ 里的 userinfo 段。
var urlUserinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]*@`)

// maskURLUserinfo 把字符串里 URL 形态的 userinfo 换成占位符，用于错误文案回显。
//
// 触发点：net/url 的解析错误会把**整个输入**回显进错误串——实测
// `parse "trojan-go://shortpw@example.com:443/%zz": invalid URL escape "%zz"`，
// 密码就在里面。这条错误经 url/option.go 的 common.NewError("failed to parse URL").Base(err)
// 冒到 main.go 打日志，于是 -url 打错一次就在日志里明文交出一个密码。
//
// 为什么必须结构性地不嵌原文，而不是下游靠 SanitizeString 兜：本仓的兜底规则要 32 位以上
// 连续字母数字，键名规则要命中 password/token 这类词，而 Trojan 的密码完全允许是 8~16 位
// 短串——两者都够不着，一路明文。AGENTS.md 早就写了「日志的凭据脱敏是尽力而为，不要当护栏用」，
// 凭据要保密就别把它写进去。诊断信息保留：占位符替掉的只有 userinfo，
// 主机、端口与真正的报错原因（哪个转义坏了）都照原样在文案里。
func maskURLUserinfo(s string) string {
	return urlUserinfoRe.ReplaceAllString(s, "${1}[REDACTED]@")
}

func NewShareInfoFromURL(shareLink string) (info ShareInfo, e error) {
	// share link must be valid url
	parse, e := neturl.Parse(shareLink)
	if e != nil {
		e = fmt.Errorf("invalid url: %s", maskURLUserinfo(e.Error()))
		return
	}

	// share link must have `trojan-go://` scheme
	if parse.Scheme != "trojan-go" {
		e = errors.New("url does not have a trojan-go:// scheme")
		return
	}

	// password
	if info.TrojanPassword = parse.User.Username(); info.TrojanPassword == "" {
		e = errors.New("no password specified")
		return
	} else if _, hasPassword := parse.User.Password(); hasPassword {
		e = errors.New("password possibly missing percentage encoding for colon")
		return
	}

	// trojanHost: not empty & strip [] from IPv6 addresses
	if info.TrojanHost = parse.Hostname(); info.TrojanHost == "" {
		e = errors.New("host is empty")
		return
	}

	// port
	if info.Port, e = handleTrojanPort(parse.Port()); e != nil {
		return
	}

	// strictly parse the query
	query, e := neturl.ParseQuery(parse.RawQuery)
	if e != nil {
		return
	}

	// sni
	if SNIs, ok := query["sni"]; !ok {
		info.SNI = info.TrojanHost
	} else if len(SNIs) > 1 {
		e = errors.New("multiple SNIs")
		return
	} else if info.SNI = SNIs[0]; info.SNI == "" {
		e = errors.New("empty SNI")
		return
	}

	// type
	if types, ok := query["type"]; !ok {
		info.Type = ShareInfoTypeOriginal
	} else if len(types) > 1 {
		e = errors.New("multiple transport types")
		return
	} else if info.Type = types[0]; info.Type == "" {
		e = errors.New("empty transport type")
		return
	} else if _, ok := validTypes[info.Type]; !ok {
		e = fmt.Errorf("unknown transport type: %s", info.Type)
		return
	}

	// host
	if hosts, ok := query["host"]; !ok {
		info.Host = info.TrojanHost
	} else if len(hosts) > 1 {
		e = errors.New("multiple hosts")
		return
	} else if info.Host = hosts[0]; info.Host == "" {
		e = errors.New("empty host")
		return
	}

	// path
	if info.Type == ShareInfoTypeWebSocket {
		if paths, ok := query["path"]; !ok {
			e = errors.New("path is required in websocket")
			return
		} else if len(paths) > 1 {
			e = errors.New("multiple paths")
			return
		} else if info.Path = paths[0]; info.Path == "" {
			e = errors.New("empty path")
			return
		}

		if !strings.HasPrefix(info.Path, "/") {
			e = errors.New("path must start with /")
			return
		}
	}

	// encryption
	if encryptionArr, ok := query["encryption"]; !ok {
		// no encryption. that's okay.
	} else if len(encryptionArr) > 1 {
		e = errors.New("multiple encryption fields")
		return
	} else if info.Encryption = encryptionArr[0]; info.Encryption == "" {
		e = errors.New("empty encryption")
		return
	} else {
		// strings.Cut 切出 "provider;params" 两段；无分号时 params 为空串，与原 SplitN 语义一致
		encryptionProviderName, encryptionParams, _ := strings.Cut(info.Encryption, ";")

		if _, ok := validEncryptionProviders[encryptionProviderName]; !ok {
			e = fmt.Errorf("unsupported encryption provider name: %s", encryptionProviderName)
			return
		}

		if encryptionProviderName == "ss" {
			ssMethod, ssPassword, found := strings.Cut(encryptionParams, ":")
			if !found {
				e = errors.New("missing ss password")
				return
			}
			if _, ok := validSSEncryptionMap[ssMethod]; !ok {
				e = fmt.Errorf("unsupported ss method: %s", ssMethod)
				return
			}

			if ssPassword == "" {
				e = errors.New("ss password cannot be empty")
				return
			}
		}
	}

	// plugin
	if plugins, ok := query["plugin"]; !ok {
		// no plugin. that's okay.
	} else if len(plugins) > 1 {
		e = errors.New("multiple plugins")
		return
	} else if info.Plugin = plugins[0]; info.Plugin == "" {
		e = errors.New("empty plugin")
		return
	}

	// description
	info.Description = parse.Fragment

	return
}

func handleTrojanPort(p string) (port uint16, e error) {
	if p == "" {
		return 443, nil
	}

	portParsed, e := strconv.Atoi(p)
	if e != nil {
		return
	}

	if portParsed < 1 || portParsed > 65535 {
		e = fmt.Errorf("invalid port %d", portParsed)
		return
	}

	port = uint16(portParsed)
	return
}
