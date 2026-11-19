package tls

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 谓词有牙不等于接线有牙：cipher 必须在 tls client/server 两个建栈点都校验，
// 否则配错的 cipher 名字仍会被 ParseCipher 静默丢弃、走 Go 默认全套。
// 判据形态同 brutal/curve 的接线门禁——读源码断言调用存在，避免构造握手夹具的 flaky。
func TestCipherValidationIsWiredIntoBothStackBuilders(t *testing.T) {
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
		if !strings.Contains(string(src), "ValidateCipher(") {
			t.Errorf("%s 没有调用 ValidateCipher：配错的 cipher 名字又会被静默丢成 Go 默认全套", file)
		}
	}
}
