package proxy

import (
	"context"
	"strings"
	"testing"
)

// TestCreatorReturningNilReturnsError 钉住公共 API RegisterProxyCreator 的防御：
// 外部 creator 违约返回 (nil, nil) 时，NewProxyFromConfigData 原先直接解引用
// p.sigChan → SIGSEGV，把一次配置错误变成整个进程崩溃。应返回可读错误。
// 变异对照：去掉 nil 检查，本用例 panic 转红。
func TestCreatorReturningNilReturnsError(t *testing.T) {
	const name = "NIL-CREATOR-TEST"
	RegisterProxyCreator(name, func(context.Context) (*Proxy, error) { return nil, nil })

	p, err := NewProxyFromConfigData([]byte(`{"run_type":"`+name+`"}`), true)
	if err == nil {
		if p != nil {
			t.Fatal("creator 返回 nil 却拿到了非 nil Proxy")
		}
		t.Fatal("creator 返回 (nil,nil) 应报错，而不是 nil,nil")
	}
	if !strings.Contains(err.Error(), "returned nil") {
		t.Fatalf("错误信息应指出 creator 返回 nil：%v", err)
	}
}
