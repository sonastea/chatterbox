package box

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/store"
)

type Config struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	TLSCert      string
	TLSKey       string
}

type Server struct {
	server *http.Server
	config *Config
	hub    *Hub
}

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		// Returning true for now, but should check origin.
		CheckOrigin: func(r *http.Request) bool {
			slog.InfoContext(r.Context(), "websocket connection origin", "origin", r.Header.Get("Origin"))
			return true
		},
	}
)

func NewServer(ctx context.Context, cfg *Config, bus broker.Broker, roomStore store.RoomRepository, userStore store.UserRepository) (*Server, error) {
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return nil, fmt.Errorf("TLS certificate and key must be configured together")
	}
	hub, err := NewHub(ctx, bus, roomStore, userStore)
	if err != nil {
		return nil, err
	}

	router := http.NewServeMux()
	router.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	}))

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		ErrorLog:     slog.NewLogLogger(slog.Default().With("component", "http").Handler(), slog.LevelError),
	}

	return &Server{server: srv, config: cfg, hub: hub}, nil
}

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.WarnContext(r.Context(), "websocket upgrade failed", "error", err)
		return
	}
	newId := xid.New().String()

	client := &Client{
		User: store.User{
			Xid:      newId,
			Name:     newId,
			Email:    newId + "@example.com",
			Password: "",
		},
		hub:        hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		done:       make(chan struct{}),
		registered: make(chan struct{}),
	}

	select {
	case client.hub.register <- client:
	case <-hub.ctx.Done():
		client.close()
		return
	}

	go client.writePump()
	go client.readPump()
}

func (s *Server) Start(ctx context.Context) error {
	transport := "HTTP/WS, app-level TLS disabled"
	if s.config.TLSCert != "" {
		transport = "HTTPS/WSS, TLS terminated by app"
	}
	slog.InfoContext(ctx, "chatterbox is now listening", "address", s.server.Addr,
		"transport", transport, "tls.enabled", s.config.TLSCert != "")
	result := make(chan error, 1)
	go func() {
		if s.config.TLSCert != "" {
			result <- s.server.ListenAndServeTLS(s.config.TLSCert, s.config.TLSKey)
		} else {
			result <- s.server.ListenAndServe()
		}
	}()
	var serveErr error
	select {
	case <-ctx.Done():
	case <-s.hub.done:
		if ctx.Err() == nil {
			serveErr = fmt.Errorf("chat hub stopped unexpectedly")
		}
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return errors.Join(serveErr, s.server.Shutdown(shutdownCtx), s.hub.Close())
}

func (s *Server) Close() error {
	return errors.Join(s.server.Close(), s.hub.Close())
}
