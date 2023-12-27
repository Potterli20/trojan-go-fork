//go:build custom || full
// +build custom full

package build

import (
	_ "github.com/Potterli20/trojan-go/proxy/custom"
	_ "github.com/Potterli20/trojan-go/tunnel/adapter"
	_ "github.com/Potterli20/trojan-go/tunnel/dokodemo"
	_ "github.com/Potterli20/trojan-go/tunnel/freedom"
	_ "github.com/Potterli20/trojan-go/tunnel/http"
	_ "github.com/Potterli20/trojan-go/tunnel/mux"
	_ "github.com/Potterli20/trojan-go/tunnel/router"
	_ "github.com/Potterli20/trojan-go/tunnel/shadowsocks"
	_ "github.com/Potterli20/trojan-go/tunnel/simplesocks"
	_ "github.com/Potterli20/trojan-go/tunnel/socks"
	_ "github.com/Potterli20/trojan-go/tunnel/tls"
	_ "github.com/Potterli20/trojan-go/tunnel/tproxy"
	_ "github.com/Potterli20/trojan-go/tunnel/transport"
	_ "github.com/Potterli20/trojan-go/tunnel/trojan"
	_ "github.com/Potterli20/trojan-go/tunnel/websocket"
)
