package box

import (
	"fmt"
	"net/http"
	"time"
)

type Config struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type Server struct {
	server *http.Server
	config *Config
}

func NewServer(cfg *Config) *Server {
	hub := NewHub()
	go hub.Run()

	router := http.NewServeMux()
	router.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWs(hub, w, r)
	}))

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	s := &Server{server: srv, config: cfg}

	return s
}

func (s *Server) Start() {
	fmt.Printf("Listening on %s\n", s.server.Addr)
	s.server.ListenAndServe()
}
