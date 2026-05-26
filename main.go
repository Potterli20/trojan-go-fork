package main

import (
	"flag"
	"os"
	"runtime"
	"time"

	_ "github.com/Potterli20/trojan-go-fork/component"
	"github.com/Potterli20/trojan-go-fork/constant"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/option"
)

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

	handlerCount := 0
	for {
		log.Debug("Attempting to get option handler... (attempt:", handlerCount+1, ")")
		h, err := option.PopOptionHandler()
		if err != nil {
			log.Error("Failed to get option handler:", err)
			log.Fatal("Invalid options - no valid handler found")
		}

		log.Info("Found handler:", h.Name(), "with priority:", h.Priority())

		log.Info("==================================================")
		log.Info("Starting handler:", h.Name())
		handlerStartTime := time.Now()

		err = h.Handle()
		if err == nil {
			handlerDuration := time.Since(handlerStartTime)
			log.Info("Handler", h.Name(), "completed successfully in", handlerDuration)
			log.Info("==================================================")
			break
		}

		handlerDuration := time.Since(handlerStartTime)
		log.Warn("Handler", h.Name(), "failed after", handlerDuration, ":", err)
		log.Warn("Trying next handler...")
		handlerCount++
	}

	totalDuration := time.Since(startTime)
	log.Info("==================================================")
	log.Info("Trojan-Go Fork startup completed")
	log.Info("Total startup time:", totalDuration)
	log.Info("==================================================")
}
