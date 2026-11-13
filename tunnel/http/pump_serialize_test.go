package http

import (
	"os"
	"strings"
	"testing"
)

// 泵协程与下一请求共用 reqBufReader：源码里"启动泵"与"下一轮 ReadRequest"
// 之间必须有 pumpDone 等待。端到端竞态判据是概率性的（要 -race 撞窗口），
// 结构性门禁钉接线本身，口径同 h2_status_clamp_test.go 的先例。
func TestPumpDrainedBeforeNextRequest(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	pump := strings.Index(s, "reqToWrite.Write(reqWriter)")
	next := strings.Index(s, "req, err = http.ReadRequest(reqBufReader)")
	if pump < 0 || next < 0 || pump > next {
		t.Fatal("锚点漂移：重新定位泵与下一请求读")
	}
	between := s[pump:next]
	if !strings.Contains(between, "<-pumpDone") {
		t.Fatal("下一请求读之前没有等泵退出：共享 bufio 存在并发读写窗口")
	}
	if !strings.Contains(s, "defer close(pumpDone)") {
		t.Fatal("泵没有退出信号")
	}
}