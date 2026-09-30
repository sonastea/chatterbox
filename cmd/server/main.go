package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sonastea/chatterbox/internal/configs"
	"github.com/sonastea/chatterbox/internal/pkg/box"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/logging"
	"github.com/sonastea/chatterbox/internal/pkg/store"
)

func main() {
	slog.SetDefault(slog.New(logging.NewHandler(os.Stderr, os.Getenv("OTEL_SERVICE_NAME"))))
	if err := run(); err != nil {
		slog.Log(context.Background(), logging.LevelFatal, "chatterbox stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := configs.NewConfig()
	if err != nil {
		return err
	}

	srvCfg, err := cfg.HTTP()
	if err != nil {
		return err
	}

	startupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	db, err := database.Open(startupCtx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()

	bus, err := broker.Open(startupCtx, cfg.Broker)
	if err != nil {
		return err
	}
	defer bus.Close()

	server, err := box.NewServer(ctx, srvCfg, bus, &store.RoomStore{DB: db}, &store.UserStore{DB: db})
	if err != nil {
		return err
	}
	defer server.Close()

	return server.Start(ctx)
}
