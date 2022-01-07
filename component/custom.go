//go:build custom || full
// +build custom full

package build

import (
	_ "github.com/Potterli20/trojan-go-fork/proxy/custom"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/adapter"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/dokodemo"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/freedom"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/http"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/mux"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/router"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/shadowsocks"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/simplesocks"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/socks"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/tls"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/tproxy"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/transport"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/trojan"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/websocket"
)
