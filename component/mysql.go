//go:build mysql || full || mini
// +build mysql full mini

package build

import (
	_ "github.com/Potterli20/trojan-go/statistic/mysql"
)
