//go:build api || full
// +build api full

package build

import (
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/api/control"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/api/service"
)
