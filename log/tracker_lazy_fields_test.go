package log

import (
	"testing"
)

// TestTrackerFieldsAreAllocatedOnDemand 盯 tracker 的每连接分配。
//
// ConnectionTracker 是每条连接、每条 mux 流各一个（mux/client.go 一条流就用两个：
// 一个 DialConn 一个 Stream），构造时无条件 make(map[string]any) 只是为了一句
// WithField("mux_id", ...)。而 Success/Error 的打印整段包在 ShouldLog 里，
// 默认级别（Info 以上）下那个 map 建了、塞了、从来没人读——纯白付。
func TestTrackerFieldsAreAllocatedOnDemand(t *testing.T) {
	tr := NewConnectionTracker("TEST", "Action")
	if tr.fields != nil {
		t.Fatal("没设任何字段就分配了 map：每条连接/每条 mux 流都要白付一份")
	}
	// 只读路径不能被 nil map 弄崩
	if err := tr.Success(); err != nil {
		t.Fatalf("fields 为 nil 时 Success 报错：%v", err)
	}
	tr.Destroy("closed", 1, 2) // nil fields 不能让关停路径炸掉

	tr2 := NewConnectionTrackerWithID("conn-x", "TEST", "Action").WithField("k", "v")
	if tr2.fields == nil {
		t.Fatal("WithField 之后仍然没有 fields：字段被静默丢弃")
	}
	if tr2.fields["k"] != "v" {
		t.Fatalf("字段值不对：%v", tr2.fields["k"])
	}

	tr3 := NewConnectionTracker("TEST", "Action").WithFields(map[string]any{"a": 1})
	if tr3.fields["a"] != 1 {
		t.Fatal("WithFields 没能建立字段表")
	}
}
