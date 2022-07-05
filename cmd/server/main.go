package main

import (
	"flag"

	"github.com/sonastea/chatterbox/internal/config"
	"github.com/sonastea/chatterbox/internal/pkg/box"
)

func main() {
	flag.Parse()

	cfg, err := config.NewConfig()
	if err != nil {
		return
	}

	srvCfg, err := cfg.HTTP()
	if err != nil {
		return
	}

	server := box.NewServer(srvCfg)

	server.Start()
}
