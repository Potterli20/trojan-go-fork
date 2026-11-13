package http

import (
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// 恶意/故障目标站发一条超过预算、永无换行的状态行：解析必须失败，
// 且读过的字节数被预算卡住（旧实现 ReadResponse 无上限，会一路读到 EOF）。
func TestResponseHeadBudgetEnforced(t *testing.T) {
	cr := &countingReader{r: strings.NewReader("HTTP/1.1 200 " + strings.Repeat("x", 4<<20))}
	_, err := readResponseBounded(cr, nil)
	if err == nil {
		t.Fatal("超预算状态行必须解析失败")
	}
	if cr.n > maxResponseHeadBytes+4096 { // bufio 填充 slack
		t.Fatalf("读了 %d 字节，预算 %d 未被执行", cr.n, maxResponseHeadBytes)
	}
	if len(err.Error()) > 512 {
		t.Fatalf("错误文案带原文超长：%d 字节", len(err.Error()))
	}
}

// 正常响应必须不受影响（预算在解析成功后解除，body 可超过头部预算）。
func TestResponseHeadBudgetAllowsNormalResponse(t *testing.T) {
	body := strings.Repeat("y", 2<<20)
	raw := "HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	resp, err := readResponseBounded(strings.NewReader(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil || len(got) != len(body) {
		t.Fatalf("body 被预算误伤: n=%d err=%v", len(got), err)
	}
}

// 生产代码里 http.ReadResponse 只允许出现在 helper 内部一处。
func TestEveryReadResponseSiteIsBounded(t *testing.T) {
	for _, f := range []string{"server.go", "h2.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "http.ReadResponse(") {
			t.Fatalf("%s 仍有裸 ReadResponse 调用点，必须走 readResponseBounded", f)
		}
	}
}