package custom

import (
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/proxy"
	_ "github.com/Potterli20/trojan-go-fork/tunnel/freedom"   // 注册 freedom（出站自拨号根，正向对照）
	_ "github.com/Potterli20/trojan-go-fork/tunnel/transport" // 注册 transport（入站根节点层）
	_ "github.com/Potterli20/trojan-go-fork/tunnel/trojan"    // 注册 trojan（需要 underlay 的层）
)

// TestOutboundRootWithoutUnderlayReturnsError 钉住 custom 出站链的根节点防御：
// 入站根有 rootCanStandAlone 校验，出站却没有镜像校验。outbound.path[0] 是链的
// 最内层，client 初值为 nil，直接 NewClient(ctx, nil)。若首层配成需要 underlay
// 的协议（trojan 等），其 NewClient 只把 nil 存进 underlay、不立即解引用，于是
// **建栈成功、返回 Proxy**；直到首个入站连接到达，relay 调 DialConn 才对 nil
// 解引用 SIGSEGV，且发生在无 recover 的中继协程里 → 整个进程崩溃。
//
// 出站能当根的是"自拨号层"（FREEDOM/TRANSPORT/QUIC），与入站监听层集合不同，
// 不能复用 rootCanStandAlone。
// 判据：trojan 作出站首层必须返回可读错误；freedom 作首层必须仍能建栈成功
// （正向对照，防止把合法配置也拒掉）。变异对照：去掉出站根校验，本用例转红。
func TestOutboundRootWithoutUnderlayReturnsError(t *testing.T) {
	cfg := `{
		"run_type":"custom",
		"inbound":{
			"node":[{"protocol":"transport","tag":"in","config":{"local_addr":"127.0.0.1","local_port":11081,"remote_addr":"127.0.0.1","remote_port":11081}}],
			"path":[["in"]]
		},
		"outbound":{
			"node":[{"protocol":"trojan","tag":"out","config":{"password":["0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"]}}],
			"path":[["out"]]
		}
	}`
	_, err := proxy.NewProxyFromConfigData([]byte(cfg), true)
	if err == nil {
		// 注意这里刻意不 Close 那个 Proxy：未修复时它的 trojan.Client 攥着 nil
		// underlay，Close 会再次 SIGSEGV 把整个测试二进制打崩，判据就变成了
		// "test binary crash" 而不是可读的红。直接判失败即可。
		t.Fatal("出站首层配成需要 underlay 的 trojan 应报错，而不是建栈成功（运行期会 SIGSEGV）")
	}
	if !strings.Contains(err.Error(), "underlay") {
		t.Fatalf("错误信息应指出出站根节点不能是无 underlay 的协议：%v", err)
	}
}

// TestOutboundSelfDialingRootStillWorks 是正向对照：自拨号层（freedom）作出站
// 首层是合法的，不能被根校验误拒。
func TestOutboundSelfDialingRootStillWorks(t *testing.T) {
	cfg := `{
		"run_type":"custom",
		"inbound":{
			"node":[{"protocol":"transport","tag":"in","config":{"local_addr":"127.0.0.1","local_port":11082,"remote_addr":"127.0.0.1","remote_port":11082}}],
			"path":[["in"]]
		},
		"outbound":{
			"node":[{"protocol":"freedom","tag":"out","config":{}}],
			"path":[["out"]]
		}
	}`
	p, err := proxy.NewProxyFromConfigData([]byte(cfg), true)
	if err != nil {
		t.Fatalf("自拨号层 freedom 作出站首层应被允许：%v", err)
	}
	if p != nil {
		p.Close() //gosec:disable -- 错误忽略：测试清理
	}
}
