//go:build server || full || mini

package build

import (
	_ "github.com/Potterli20/trojan-go-fork/proxy/server"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/quic"
)
