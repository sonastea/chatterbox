package main

import (
	"flag"
	"log"

	"github.com/sonastea/chatterbox/internal/configs"
	"github.com/sonastea/chatterbox/internal/pkg/box"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/store"
)

func main() {
	flag.Parse()

	cfg, err := configs.NewConfig()
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

	db := database.NewConnPool()
	box.InitRedisClient(cfg.RedisOpt)

	server := box.NewServer(srvCfg, &store.RoomStore{DB: db}, &store.UserStore{DB: db})

	server.Start()
}
