package server

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sonastea/chatterbox/internal/pkg/websocket"
)

type Config struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type Server struct {
	server *http.Server
	mux    *chi.Mux
	config *Config
}

func NewServer(cfg *Config) *Server {
	r := chi.NewRouter()

	r.Mount("/ws", wsRouter())

	s := &Server{
		server: &http.Server{
			Addr:         cfg.Addr,
			Handler:      r,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
		mux: r,
	}

	return s
}

func (s *Server) Start() {
	fmt.Printf("Listening on %s\n", s.server.Addr)
	s.server.ListenAndServe()
}

func wsRouter() http.Handler {
	r := chi.NewRouter()

	r.Get("/chat", chat)

	return r
}

func chat(w http.ResponseWriter, r *http.Request) {
	pool := websocket.NewPool()
	go pool.Start()
	websocket.ServeWs(pool, w, r)

	c, err := websocket.Upgrade(w, r)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("recv: %s", message)
		err = c.WriteMessage(mt, message)
		if err != nil {
			log.Println("write:", err)
			break
		}
	}
}
