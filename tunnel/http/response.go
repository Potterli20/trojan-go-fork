package http

import (
	"bufio"
	"io"
	"net/http"

	"github.com/Potterli20/trojan-go-fork/common"
)

// maxResponseHeadBytes 与请求侧同档（1MB）。ReadResponse 自身没有行长/头字节
// 上限，而响应来自目标站（可能被控/故障），不卡预算就是按对端输入分配内存。
const maxResponseHeadBytes = 1 << 20

// truncateForLog 把解析错误里回显的原文截到 256 字节：stdlib 会把整条畸形
// 状态行/头行塞进错误串，预算内仍可能有 1MB，进日志就是日志放大。
func truncateForLog(s string) string {
	const limit = 256
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…(truncated)"
}

// readResponseBounded 是 h1/h2 两条路唯一的响应解析入口。预算只覆盖头部解析
// 窗口，解析成功立刻解除——body 合法地可以远超头部预算。
func readResponseBounded(r io.Reader, req *http.Request) (*http.Response, error) {
	bounded := common.NewBoundedReader(r)
	bounded.Limit(maxResponseHeadBytes)
	resp, err := http.ReadResponse(bufio.NewReader(bounded), req)
	bounded.Unlimited()
	if err != nil {
		return nil, common.NewError("failed to read response head: " + truncateForLog(err.Error()))
	}
	return resp, nil
}
