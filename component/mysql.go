//go:build mysql || full || mini
// +build mysql full mini

package build

import (
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/statistic/mysql"
)
