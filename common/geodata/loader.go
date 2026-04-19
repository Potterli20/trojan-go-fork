package geodata

import (
	"runtime"

	v2router "github.com/xtls/xray-core/app/router"
	v2geodata "github.com/xtls/xray-core/common/geodata"
)

type geodataCache struct {
	geoipCache
	geositeCache
}

func NewGeodataLoader() GeodataLoader {
	return &geodataCache{
		make(map[string]*v2geodata.GeoIP),
		make(map[string]*v2geodata.GeoSite),
	}
}

func (g *geodataCache) LoadIP(filename, country string) ([]*v2router.CIDR, error) {
	geoip, err := g.geoipCache.Unmarshal(filename, country)
	if err != nil {
		return nil, err
	}
	runtime.GC()
	return geoip.GetCidr(), nil
}

func (g *geodataCache) LoadSite(filename, list string) ([]*v2router.Domain, error) {
	geosite, err := g.geositeCache.Unmarshal(filename, list)
	if err != nil {
		return nil, err
	}
	runtime.GC()
	return geosite.GetDomain(), nil
}

func (g *geodataCache) LoadGeoIP(country string) ([]*v2router.CIDR, error) {
	return g.LoadIP("geoip.dat", country)
}

func (g *geodataCache) LoadGeoSite(list string) ([]*v2router.Domain, error) {
	return g.LoadSite("geosite.dat", list)
}
