package router

import (
	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
)

type Config struct {
	Router RouterConfig `json:"router" yaml:"router"`
}

type RouterConfig struct {
	Enabled         bool     `json:"enabled" yaml:"enabled"`
	Bypass          []string `json:"bypass" yaml:"bypass"`
	Proxy           []string `json:"proxy" yaml:"proxy"`
	Block           []string `json:"block" yaml:"block"`
	DomainStrategy  string   `json:"domain_strategy" yaml:"domain-strategy"`
	DefaultPolicy   string   `json:"default_policy" yaml:"default-policy"`
	GeoIPFilename   string   `json:"geoip" yaml:"geoip"`
	GeoSiteFilename string   `json:"geosite" yaml:"geosite"`
}

// assetLocation 解析 geo 数据文件路径;失败时回退裸文件名——
// 加载期 common/geodata/cache.go 会再次解析,届时失败也只会跳过
// 对应规则并告警,不应在 init 阶段以 Fatal 杀死整个进程
func assetLocation(file string) string {
	path, err := common.GetAssetLocation(file)
	if err != nil {
		log.Warn(common.NewError("failed to resolve asset location, fallback to bare filename").Base(err))
		return file
	}
	return path
}

func init() {
	config.RegisterConfigCreator(Name, func() any {
		cfg := &Config{
			Router: RouterConfig{
				DefaultPolicy:   "proxy",
				DomainStrategy:  "as_is",
				GeoIPFilename:   assetLocation("geoip.dat"),
				GeoSiteFilename: assetLocation("geosite.dat"),
			},
		}
		return cfg
	})
}
