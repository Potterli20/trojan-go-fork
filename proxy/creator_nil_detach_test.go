package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/log"
)

// creator 违约返回 (nil,nil) 的分支是 bd295baf 后加的防御：当时只 signal.Stop +
// return error，漏了 log_file 的 detach。err!=nil 分支（TestNewProxyFailureLeaves-
// LogOutputUsable 守着）会 detachLogFile，两条失败出口却只有一条交还输出。
// 后果：库化调用方拿到 error 后打的那行日志全落进已关闭的文件，静默丢失。
func TestNilCreatorFailureLeavesLogOutputUsable(t *testing.T) {
	capture := detachTestEnv(t)

	const marker = "NIL-CREATOR-DETACH-MARKER"
	logPath := t.TempDir() + "/agent.log"

	const name = "NIL-CREATOR-DETACH-TEST"
	RegisterProxyCreator(name, func(context.Context) (*Proxy, error) { return nil, nil })
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		delete(creators, name)
	})

	data := []byte(`{"run_type":"` + name + `","log_level":0,"log_file":"` + logPath + `"}`)
	p, err := NewProxyFromConfigData(data, true)
	if err == nil {
		if p != nil {
			_ = p.Close()
		}
		t.Fatal("creator 返回 (nil,nil) 必须报错，否则这条判据没走到失败路径")
	}

	// 复刻 main 拿到错误后的报错路径
	log.Error(common.NewError(marker))

	if got := readCaptured(t, capture); !strings.Contains(got, marker) {
		t.Fatalf("(nil,nil) creator 失败后 main 的报错被吞掉：输出仍指着已关闭的日志文件，stderr 收到 %q", got)
	}
}
