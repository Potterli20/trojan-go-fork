//go:build custom || full
// +build custom full

package build

import (
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/proxy/custom"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/adapter"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/dokodemo"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/freedom"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/http"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/mux"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/router"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/shadowsocks"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/simplesocks"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/socks"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/tls"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/tproxy"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/transport"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/trojan"
	_ "gitlab.atcatw.org/atca/community-edition/trojan-go.git/tunnel/websocket"
)
