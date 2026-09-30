package configs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sonastea/chatterbox/internal/pkg/box"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/database"
)

type Configs struct {
	Database database.Config
	Broker   broker.Config
}

func (cfg *Configs) HTTP() (*box.Config, error) {
	cert, key := os.Getenv("TLS_CERT"), os.Getenv("TLS_KEY")
	if (cert == "") != (key == "") {
		return nil, fmt.Errorf("TLS_CERT and TLS_KEY must be configured together")
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8443"
	}
	return &box.Config{
		Addr:         addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		TLSCert:      cert,
		TLSKey:       key,
	}, nil
}

func NewConfig() (*Configs, error) {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("BROKER_DRIVER")))
	if driver == "" {
		driver = "memory"
	}
	address := os.Getenv("BROKER_URL")
	if address == "" {
		switch driver {
		case "redis":
			address = os.Getenv("REDIS_URL")
		case "valkey":
			address = os.Getenv("VALKEY_URL")
			if address == "" {
				address = os.Getenv("REDIS_URL")
			}
		case "nats":
			address = os.Getenv("NATS_URL")
		case "rabbitmq":
			address = os.Getenv("RABBITMQ_URL")
			if address == "" {
				address = os.Getenv("AMQP_URL")
			}
		}
	}
	return &Configs{
		Database: database.Config{Driver: os.Getenv("DATABASE_DRIVER"), URL: os.Getenv("DATABASE_URL")},
		Broker:   broker.Config{Driver: driver, URL: address},
	}, nil
}
