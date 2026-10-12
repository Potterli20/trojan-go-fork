package main

import (
	"flag"
	"os"
	"runtime"
	"time"

	"github.com/Potterli20/trojan-go-fork/common"
	_ "github.com/Potterli20/trojan-go-fork/component"
	"github.com/Potterli20/trojan-go-fork/constant"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/option"
)

// selectAndRun 按优先级逐个试 handler，直到有一个接管本次运行：
//   - Handle() 返回 nil            ⇒ 它接管了，正常返回
//   - 返回 option.IsNotSet 的错误  ⇒ 命令行里没有它的参数，继续试下一个
//   - 返回其它错误                 ⇒ 参数确实是它的、但执行失败，立刻把原因报出去
//
// 最后一条是这里的关键。之前的写法是「任何错误都试下一个」，于是一份坏配置会一路
// 落到队尾，最后只剩一句 "Invalid options - no valid handler found"，把真正的原因
// （JSON 解析失败、地址不合法、端口被占……）降级成中间一行 WARN——而 -log-level
// 调高时那行 WARN 都不会出现。
func selectAndRun(pop func() (option.Handler, error)) error {
	for {
		h, err := pop()
		if err != nil {
			return common.NewError("no handler accepted the given command line options").Base(err)
		}
		log.Info("Found handler:", h.Name(), "with priority:", h.Priority())
		start := time.Now()
		err = h.Handle()
		switch {
		case err == nil:
			log.Info("Handler", h.Name(), "completed successfully in", time.Since(start))
			return nil
		case option.IsNotSet(err):
			log.Debug("Handler", h.Name(), "not selected by the command line")
		default:
			log.Warn("Handler", h.Name(), "failed after", time.Since(start), ":", err)
			return err
		}
	}
}

func main() {
	startTime := time.Now()

	log.Info("==================================================")
	log.Info("Trojan-Go Fork Starting...")
	log.Info("Version:", constant.Version)
	log.Info("Commit:", constant.Commit)
	log.Info("Go Version:", runtime.Version())
	log.Info("OS/Arch:", runtime.GOOS, "/", runtime.GOARCH)
	log.Info("PID:", os.Getpid())
	log.Info("Start Time:", startTime.Format("2006-01-02 15:04:05"))
	log.Info("==================================================")

	flag.Parse()
	log.Debug("Command line flags parsed successfully")

	if err := selectAndRun(option.PopOptionHandler); err != nil {
		log.Error(err)
		log.Fatal("Invalid options - failed to start")
	}

	totalDuration := time.Since(startTime)
	log.Info("==================================================")
	log.Info("Trojan-Go Fork startup completed")
	log.Info("Total startup time:", totalDuration)
	log.Info("==================================================")
}
