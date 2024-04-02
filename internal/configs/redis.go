package configs

import (
	"log"
	"os"

	"github.com/redis/go-redis/v9"
)

func NewRedisConfig() *redis.Options {
	opt, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Fatal(err)
	}

	return opt
}
