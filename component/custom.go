//go:build custom || full
// +build custom full

package build

import (
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/proxy/custom"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/adapter"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/dokodemo"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/freedom"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/http"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/mux"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/router"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/shadowsocks"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/simplesocks"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/socks"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/tls"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/tproxy"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/transport"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/trojan"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go/tunnel/websocket"
)
