package router

import (
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/common"
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/config"
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

func init() {
	config.RegisterConfigCreator(Name, func() any {
		cfg := &Config{
			Router: RouterConfig{
				DefaultPolicy:   "proxy",
				DomainStrategy:  "as_is",
				GeoIPFilename:   common.GetAssetLocation("geoip.dat"),
				GeoSiteFilename: common.GetAssetLocation("geosite.dat"),
			},
		}
		return cfg
	})
}
