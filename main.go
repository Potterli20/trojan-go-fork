package main

import (
	"flag"

	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/component"
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/log"
	"gitlab.atcatw.org/atca/community-edition/trojan-go.git/option"
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
