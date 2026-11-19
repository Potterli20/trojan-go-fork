package quic

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestValidateQuicParamsRejectsNegativeValues 钉住 QUIC 数值配置的合法性：
// initial_stream_window / initial_conn_window 是 int，经 uint64(cfg...) 直接
// 喂给 quic.Config：配置 -1 会变成约 1.8e19 字节的流控窗口，对端可以据此灌入
// 海量数据；max_incoming_streams 负数变 int64 负值，max_idle_timeout 负数变
// 负 duration（还会让 KeepAlivePeriod=idle/2 变负）。这几项没有任何校验，与同文件
// brutal / 曲线名的严格校验不一致。0 是合法值（窗口交给 quic-go 库默认、idle=0
// 关保活），所以闸只拒绝负值，不擅自收紧 0 的既有语义。
//
// 变异对照：去掉 ValidateQuicParams 的范围检查，负值用例立刻转红。
func TestValidateQuicParamsRejectsNegativeValues(t *testing.T) {
	valid := QUICConfig{MaxIdleTimeout: 30, MaxIncomingStreams: 100}
	for _, tc := range []struct {
		name string
		cfg  QUICConfig
	}{
		{"负的 initial_stream_window", QUICConfig{MaxIdleTimeout: 30, MaxIncomingStreams: 100, InitialStreamWindow: -1}},
		{"负的 initial_conn_window", QUICConfig{MaxIdleTimeout: 30, MaxIncomingStreams: 100, InitialConnWindow: -1}},
		{"负的 max_incoming_streams", QUICConfig{MaxIdleTimeout: 30, MaxIncomingStreams: -1}},
		{"负的 max_idle_timeout", QUICConfig{MaxIdleTimeout: -1, MaxIncomingStreams: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateQuicParams(tc.cfg); err == nil {
				t.Fatal("非法 QUIC 参数必须报错")
			}
		})
	}

	// 正向对照：默认配置与"窗口 0 = 库默认"都必须放行。
	if err := ValidateQuicParams(valid); err != nil {
		t.Fatalf("默认配置不该被拦住：%v", err)
	}
	withZeroWindows := valid
	withZeroWindows.InitialStreamWindow = 0
	withZeroWindows.InitialConnWindow = 0
	if err := ValidateQuicParams(withZeroWindows); err != nil {
		t.Fatalf("窗口为 0（库默认）不该被拦住：%v", err)
	}
	withPositiveWindows := valid
	withPositiveWindows.InitialStreamWindow = 2 << 20
	withPositiveWindows.InitialConnWindow = 3 << 20
	if err := ValidateQuicParams(withPositiveWindows); err != nil {
		t.Fatalf("显式正值窗口不该被拦住：%v", err)
	}
	zeroIdleAndStreams := QUICConfig{}
	if err := ValidateQuicParams(zeroIdleAndStreams); err != nil {
		t.Fatalf("idle=0（关保活）与 streams=0 是合法语义，不该被拦住：%v", err)
	}
}

// 谓词有牙不等于接线有牙：client 与 server 两条建栈路径都必须真的调用它，
// 否则负值照样经 uint64 转换毒化 quic.Config。
func TestQuicParamsValidationIsWiredIntoBothStackBuilders(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到本文件名，门禁失效")
	}
	dir := filepath.Dir(thisFile)
	for _, file := range []string{"client.go", "server.go"} {
		src, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("读 %s: %v", file, err)
		}
		if !strings.Contains(string(src), "ValidateQuicParams(") {
			t.Errorf("%s 没有调用 ValidateQuicParams：负值参数又会毒化 quic.Config", file)
		}
	}
}
