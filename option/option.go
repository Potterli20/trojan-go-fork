package option

import (
	"cmp"
	"maps"
	"slices"
	"sync"

	"github.com/Potterli20/trojan-go-fork/common"
)

type Handler interface {
	Name() string
	Handle() error
	Priority() int
}

// NotSetMessage 是「命令行里没有我要的参数」的统一标记。main.go 靠它区分两件
// 完全不同的事：这个 handler 不适用（继续按优先级试下一个）vs 这个 handler 确实
// 被选中了但执行失败（直接把原因报出去，不能再往下试）。
//
// 判定用字符串比较而不是 errors.Is：本项目的 common.Error 用 " | " 拼接、没有
// Unwrap 链，一旦错误被 .Base() 包过一层，errors.Is/As 就穿不过去。
const NotSetMessage = "not set"

// NotSet 返回「本 handler 不适用」的标记错误。handler 只能在自己没被命令行
// 命中时用它；参数命中后的任何失败都必须返回真实错误。
func NotSet() error {
	return common.NewError(NotSetMessage)
}

func IsNotSet(err error) bool {
	return err != nil && err.Error() == NotSetMessage
}

var (
	handlers = make(map[string]Handler)
	mu       sync.RWMutex
)

func RegisterHandler(h Handler) {
	mu.Lock()
	defer mu.Unlock()
	handlers[h.Name()] = h
}

func PopOptionHandler() (Handler, error) {
	mu.Lock()
	defer mu.Unlock()

	candidates := slices.Collect(maps.Values(handlers))
	if len(candidates) == 0 {
		return nil, common.NewError("no option handler available")
	}
	maxHandler := slices.MaxFunc(candidates, func(a, b Handler) int {
		return cmp.Compare(a.Priority(), b.Priority())
	})
	delete(handlers, maxHandler.Name())
	return maxHandler, nil
}

func GetHandler(name string) (Handler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	h, ok := handlers[name]
	return h, ok
}

func HandlerCount() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(handlers)
}
