package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/Potterli20/trojan-go-fork/option"
)

type fakeHandler struct {
	name string
	prio int
	err  error
	ran  *[]string
}

func (f fakeHandler) Name() string  { return f.name }
func (f fakeHandler) Priority() int { return f.prio }
func (f fakeHandler) Handle() error {
	if f.ran != nil {
		*f.ran = append(*f.ran, f.name)
	}
	return f.err
}

func popAll(hs ...option.Handler) func() (option.Handler, error) {
	queue := append([]option.Handler(nil), hs...)
	return func() (option.Handler, error) {
		if len(queue) == 0 {
			return nil, errors.New("empty")
		}
		h := queue[0]
		queue = queue[1:]
		return h, nil
	}
}

// 不适用（not set）的 handler 必须被跳过、继续试下一个，且跳过的不算失败。
func TestSelectAndRunSkipsNotSetHandlers(t *testing.T) {
	var ran []string
	err := selectAndRun(popAll(
		fakeHandler{name: "a", err: option.NotSet(), ran: &ran},
		fakeHandler{name: "b", err: option.NotSet(), ran: &ran},
		fakeHandler{name: "c", err: nil, ran: &ran},
	))
	if err != nil {
		t.Fatalf("第三个 handler 成功了，selectAndRun 不该返回错误：%v", err)
	}
	if strings.Join(ran, ",") != "a,b,c" {
		t.Fatalf("执行顺序不对，实际跑了 %v", ran)
	}
}

// 真失败必须立刻上报，不能再往下试——这正是本轮修的行为：以前任何错误都会
// "Trying next handler..."，一份坏配置最后只剩一句 no valid handler found。
func TestSelectAndRunStopsOnRealError(t *testing.T) {
	var ran []string
	err := selectAndRun(popAll(
		fakeHandler{name: "a", err: option.NotSet(), ran: &ran},
		fakeHandler{name: "bad", err: errors.New("failed to create proxy | invalid character"), ran: &ran},
		fakeHandler{name: "should-not-run", err: nil, ran: &ran},
	))
	if err == nil {
		t.Fatal("handler 真失败了，selectAndRun 却报告成功")
	}
	if !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("错误原因丢了，最终只剩一句通用文案：%v", err)
	}
	for _, n := range ran {
		if n == "should-not-run" {
			t.Fatal("已经确定失败还继续跑后面的 handler，会重复启动/重复绑端口")
		}
	}
}

// 一个都不适用时，报的是"命令行没匹配到任何 handler"，而不是某个 handler 的假错误。
func TestSelectAndRunReportsWhenNothingMatches(t *testing.T) {
	err := selectAndRun(popAll(
		fakeHandler{name: "a", err: option.NotSet()},
		fakeHandler{name: "b", err: option.NotSet()},
	))
	if err == nil {
		t.Fatal("没有任何 handler 接管，却返回了 nil")
	}
	if !strings.Contains(err.Error(), "no handler accepted") {
		t.Fatalf("报错文案没说明真实原因：%v", err)
	}
}

// 标记判定用的是错误文本，不是 errors.Is：common.Error 用 " | " 拼接、没有 Unwrap 链。
// 这条把"包装过的 not set 会被当成真失败"这个坑钉住——handler 绝不能把 NotSet()
// 再 .Base() 一层。
func TestNotSetIsNotMatchedThroughWrapping(t *testing.T) {
	if option.IsNotSet(nil) {
		t.Fatal("nil 不该被当成 not set")
	}
	if !option.IsNotSet(option.NotSet()) {
		t.Fatal("NotSet() 的返回值必须被识别")
	}
	if option.IsNotSet(errors.New("other")) {
		t.Fatal("别的错误不该被当成 not set")
	}
}

// TestAPIHandlerReportsFailureInsteadOfPretendingSuccess 盯 api/control 的黑洞：
// 之前它把所有失败都 log.Error 之后 `return nil`，main 把 nil 当成「这个 handler
// 接管成功」，于是 `-api list/set/...` 连不上服务端、参数不合法、甚至未知子命令，
// 退出码一律是 0 —— 脚本完全无法判断成败。修复前的 HEAD 在这里就是 nil。
func TestAPIHandlerReportsFailureInsteadOfPretendingSuccess(t *testing.T) {
	h, ok := option.GetHandler("api")
	if !ok {
		t.Skip("本次构建没有注册 api handler")
	}
	defer func() {
		flag.Set("api", "")      //gosec:disable -- 错误忽略：测试复位
		flag.Set("api-addr", "") //gosec:disable -- 错误忽略：测试复位
	}()

	if err := flag.Set("api", "list"); err != nil {
		t.Fatalf("设置 -api 失败: %v", err)
	}
	// 空 target 让 grpc.NewClient 直接失败；就算它放过去，后续 RPC 也必须报错
	if err := flag.Set("api-addr", ""); err != nil {
		t.Fatalf("设置 -api-addr 失败: %v", err)
	}
	if err := h.Handle(); err == nil {
		t.Fatal("api handler 在连不上服务端的还返回 nil：main 会当成成功、退出码 0")
	}
}

// TestEveryHandlerSaysNotSetWhenItsFlagsAreAbsent 是整条入口链的守卫：
// 只要有个 handler 在"命令行没它的参数"时返回了别的错误（或 nil），main 就会把
// 它当成失败/成功，后面的 handler 再也轮不到——用户看到的是莫名其妙的退出码或
// "no valid handler found"。
//
// PROXY（默认 handler）不在检查列表里：它没有"不适用"分支，真的会在当前目录找
// config.json 并可能起一个代理，测试里调它等于自己制造一个进程。
func TestEveryHandlerSaysNotSetWhenItsFlagsAreAbsent(t *testing.T) {
	names := []string{"version", "easy", "URL", "api", "PROXY_STDIN", "PROXY"}
	found := 0
	for _, name := range names {
		h, ok := option.GetHandler(name)
		if !ok {
			continue // 由 build tag 决定哪些 handler 存在
		}
		found++
		if name == "PROXY" {
			continue
		}
		if err := h.Handle(); !option.IsNotSet(err) {
			t.Errorf("handler %s 在参数缺省时没有返回 NotSet()，实际：%v", name, err)
		}
	}
	if found != option.HandlerCount() {
		t.Fatalf("注册表里有 %d 个 handler，但本用例只覆盖了 %d 个：%s —— 新 handler 必须一并纳入检查",
			option.HandlerCount(), found, strings.Join(names, ", "))
	}
	if found < 4 {
		t.Fatalf("只找到 %d 个 handler，多半是 build tag 不对或注册表空了（这条判据就会空转）", found)
	}
}
