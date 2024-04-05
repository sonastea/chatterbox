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
		log.Fatalf("[NewConfig]: %v\n", err)
	}

	srvCfg, err := cfg.HTTP()
	if err != nil {
		log.Fatalf("[ServerConfig] %v\n", err)
	}

	err = database.InitDB(ctx)
	if err != nil {
		log.Fatalf("[InitDB] %v\n", err)
	}

	pool := database.NewConnPool(ctx)
    defer pool.Close()

	server := box.NewServer(srvCfg, cfg.RedisOpt, &store.RoomStore{DB: pool}, &store.UserStore{DB: pool})

	server.Start(ctx)
}
