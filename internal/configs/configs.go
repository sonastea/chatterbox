package configs

import (
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sonastea/chatterbox/internal/pkg/box"
)

type Configs struct {
	RedisOpt *redis.Options
}

func (cfg *Configs) HTTP() (*box.Config, error) {
	return &box.Config{
		Addr:         ":8443",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}, nil
}

func NewConfig() (*Configs, error) {
	return &Configs{
		RedisOpt: NewRedisConfig(),
	}, nil
}
