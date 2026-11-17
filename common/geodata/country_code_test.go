package geodata

import (
	"testing"

	v2geodata "github.com/xtls/xray-core/common/geodata"
)

// countryCodeFromGeoIP 必须返回 GeoIP.Code。xray-core 的真实字段是 Code（getter
// GetCode），不是 CountryCode；旧实现反射查找 CountryCode/GetCountryCode，两者都不存在，
// 导致该函数恒返回空串，geoip 回退分支永远匹配不上任何国家码。
func TestCountryCodeFromGeoIP(t *testing.T) {
	cases := []struct {
		name string
		geo  *v2geodata.GeoIP
		want string
	}{
		{name: "code set", geo: &v2geodata.GeoIP{Code: "CN"}, want: "CN"},
		{name: "lowercase preserved", geo: &v2geodata.GeoIP{Code: "private"}, want: "private"},
		{name: "empty", geo: &v2geodata.GeoIP{}, want: ""},
		{name: "nil", geo: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countryCodeFromGeoIP(tc.geo); got != tc.want {
				t.Fatalf("countryCodeFromGeoIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
