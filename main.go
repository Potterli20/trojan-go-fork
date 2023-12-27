package main

import (
	"flag"

	_ "github.com/Potterli20/trojan-go/component"
	"github.com/Potterli20/trojan-go/log"
	"github.com/Potterli20/trojan-go/option"
)

func main() {
	flag.Parse()
	for {
		h, err := option.PopOptionHandler()
		if err != nil {
			log.Fatal("invalid options")
		}
		err = h.Handle()
		if err == nil {
			break
		}
	}
}
