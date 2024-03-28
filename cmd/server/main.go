package main

import (
	"context"
	"flag"
	"log"

	"github.com/sonastea/chatterbox/internal/configs"
	"github.com/sonastea/chatterbox/internal/pkg/box"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/store"
)

func main() {
    ctx := context.Background()
	flag.Parse()

	cfg, err := configs.NewConfig()
	if err != nil {
		return
	}

	srvCfg, err := cfg.HTTP()
	if err != nil {
		return
	}

	err = database.InitDB(ctx)
	if err != nil {
		log.Fatal(err)
	}

	db := database.NewConnPool(ctx)

	server := box.NewServer(srvCfg, cfg.RedisOpt, &store.RoomStore{DB: db}, &store.UserStore{DB: db})

	server.Start()
}
