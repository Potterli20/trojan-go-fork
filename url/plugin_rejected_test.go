package url

import (
	"strings"
	"testing"
	"time"
)

// -url 分享链接的 plugin 查询参数被 NewShareInfoFromURL 严格解析并校验（空值/重复都报错），
// 但 Handle() 构造 UrlConfig 时完全没有 plugin/transport_plugin 字段，info.Plugin 全仓
// 唯一出现就是赋值处。用户写了 ?plugin=obfs 以为插件链路启用，实际进程照常启动、
// 连接可用、没有任何告警——典型的"接受但不生效"。
//
// 本项目没有把裸 plugin 字符串映射成 transport_plugin（需要 command/arg，且分享链接
// 的 plugin 格式无约定）的安全依据，擅自拼命令有执行外部程序风险，所以正确修法是显式
// 拒绝：配了 plugin 就报错，告诉用户此入口不支持，而不是静默吞掉。
//
// 旧代码会把 plugin 静默忽略、继续建栈并 RunAndClose 阻塞；修复后在建栈前快速返回错误。
// 用超时区分二者（与 option_test.go 的同款夹具），auth 与建栈都不该发生。
func TestUrlHandleRejectsPluginParameter(t *testing.T) {
	s := "trojan-go://password@server.com/?plugin=obfs"
	opt := "mux=false;listen=127.0.0.1:0"
	u := &URLOption{urlStr: &s, options: &opt}

	errChan := make(chan error, 1)
	go func() { errChan <- u.Handle() }()

	select {
	case err := <-errChan:
		if err == nil {
			t.Fatal("plugin 参数被静默忽略：Handle 正常启动并 RunAndClose 返回了 nil")
		}
		if !strings.Contains(err.Error(), "plugin") {
			t.Fatalf("报错应明确指出 plugin 不受支持：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Handle 阻塞在建栈/运行：plugin 参数既没生效也没被显式拒绝")
	}
}
