package recorder

import (
	"os"
	"strings"
	"testing"
)

// 一个已关闭的订阅者 channel 不能让同轮其他订阅者丢记录：守护粒度必须是
// "一次发送"而不是"一轮广播"。Range 的遍历顺序不可控，所以行为断言直接钉
// 发送原语，接线正确性由下面的结构门禁钉住。
func TestBroadcastOneBadSubscriberDoesNotStarveOthers(t *testing.T) {
	bad := make(chan Record, 1)
	close(bad)
	safeSendRecord(bad, Record{}) // 不 panic 即通过

	good := make(chan Record, 1)
	safeSendRecord(good, Record{Transport: "tcp", TargetPort: "80"})
	select {
	case r := <-good:
		if r.Transport != "tcp" || r.TargetPort != "80" {
			t.Fatalf("记录内容被改写: %+v", r)
		}
	default:
		t.Fatal("正常订阅者没有收到记录")
	}
}

// broadcast 里不允许出现函数级 recover（它会让一个坏订阅者中止整轮 Range，
// 同轮其余订阅者一起挨饿）；每个订阅者的发送必须走 safeSendRecord。
func TestBroadcastGuardsPerSubscriber(t *testing.T) {
	src, err := os.ReadFile("recorder.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func broadcast(")
	if start < 0 {
		t.Fatal("锚点漂移：broadcast 未找到")
	}
	if !strings.Contains(s, "func safeSendRecord(") {
		t.Fatal("safeSendRecord 未定义")
	}
	body := s[start:]
	if end := strings.Index(body, "\n}"); end > 0 {
		body = body[:end]
	}
	if strings.Contains(body, "recover()") {
		t.Fatal("broadcast 仍有函数级 recover：一个坏订阅者会中止整轮 Range")
	}
	if !strings.Contains(body, "safeSendRecord(opt.recordChan, record)") {
		t.Fatal("broadcast 没有逐订阅者走 safeSendRecord")
	}
}