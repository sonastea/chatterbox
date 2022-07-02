package config

import (
	"time"

	"github.com/sonastea/chatterbox/internal/server"
)

type Config struct{}

func (cfg *Config) HTTP() (*server.Config, error) {
	return &server.Config{
		Addr:         ":8080",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}, nil
}

func NewConfig() (*Config, error) {
	return &Config{}, nil
}
