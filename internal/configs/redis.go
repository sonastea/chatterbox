package configs

import (
	"log"

	"github.com/go-redis/redis/v8"
)

func NewRedisConfig() *redis.Options {
	opt, err := redis.ParseURL("redis://localhost:6379/0")
	if err != nil {
		log.Fatal(err)
	}

	return opt
}
