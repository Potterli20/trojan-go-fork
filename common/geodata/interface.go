package geodata

import (
	v2geodata "github.com/xtls/xray-core/common/geodata"
)

type GeodataLoader interface {
	LoadIP(filename, country string) ([]*v2geodata.CIDR, error)
	LoadSite(filename, list string) ([]*v2geodata.Domain, error)
	LoadGeoIP(country string) ([]*v2geodata.CIDR, error)
	LoadGeoSite(list string) ([]*v2geodata.Domain, error)
}