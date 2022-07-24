package main

import (
	"flag"
	"log"

	"github.com/sonastea/chatterbox/internal/config"
	"github.com/sonastea/chatterbox/internal/pkg/box"
	"github.com/sonastea/chatterbox/internal/pkg/database"
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

	err = database.InitDB()
	if err != nil {
		log.Fatal(err)
	}

	server := box.NewServer(srvCfg)

	server.Start()
}
