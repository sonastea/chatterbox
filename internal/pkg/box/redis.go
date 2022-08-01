package box

import (
	"github.com/go-redis/redis/v8"
)

var Redis *redis.Client

func InitRedisClient(opt *redis.Options) {
	r := redis.NewClient(opt)
	Redis = r
}
