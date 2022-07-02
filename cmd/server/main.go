package main

import (
	"github.com/sonastea/chatterbox/internal/config"
	"github.com/sonastea/chatterbox/internal/server"
)

func main() {
	cfg, err := config.NewConfig()
	if err != nil {
		return
	}

	srvCfg, err := cfg.HTTP()
	if err != nil {
		return
	}

	server := server.NewServer(srvCfg)

	server.Start()
}
