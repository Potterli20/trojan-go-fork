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

func init() {
	geoipPath, err := common.GetAssetLocation("geoip.dat")
	if err != nil {
		log.Fatal(err)
	}
	geositePath, err := common.GetAssetLocation("geosite.dat")
	if err != nil {
		log.Fatal(err)
	}
	config.RegisterConfigCreator(Name, func() any {
		cfg := &Config{
			Router: RouterConfig{
				DefaultPolicy:   "proxy",
				DomainStrategy:  "as_is",
				GeoIPFilename:   geoipPath,
				GeoSiteFilename: geositePath,
			},
		}
		return cfg
	})
}
