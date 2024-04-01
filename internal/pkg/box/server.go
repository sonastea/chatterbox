package box

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/models"
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

var (
	tlsCert = ("./certs/chatterbox-cert.pem")
	tlsKey  = ("./certs/chatterbox-key.pem")

	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		// Returning true for now, but should check origin.
		CheckOrigin: func(r *http.Request) bool {
			log.Printf("Origin %v\n", r.Header.Get("Origin"))
			return true
		},
	}
)

func NewServer(cfg *Config, redisOpt *redis.Options, roomStore models.RoomStore, userStore models.UserStore) *Server {
	hub, err := NewHub(redisOpt, roomStore, userStore)
	if err != nil {
		log.Fatal(err)
	}

	go hub.Run()

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
	}

	s := &Server{server: srv, config: cfg}

	return s
}

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	newId := xid.New().String()

	client := &Client{
		Xid:      newId,
		Name:     newId,
		Email:    newId + "example.com",
		Password: "",
		hub:      hub,
		conn:     conn,
		rooms:    make(map[*Room]bool),
		send:     make(chan []byte),
	}

	client.hub.register <- client

	go client.writePump()
	go client.readPump()
}

func (s *Server) Start(ctx context.Context) {
	fmt.Printf("Listening on %s\n", s.server.Addr)
	go func() {
		if err := s.server.ListenAndServeTLS(tlsCert, tlsKey); err != http.ErrServerClosed {
			log.Fatalf("HTTPS server ListenAndServe: %v", err)
		}
	}()

	cleanup := make(chan os.Signal, 1)
	signal.Notify(cleanup, os.Interrupt, syscall.SIGINT)
	<-cleanup

	go func() {
		<-cleanup
	}()

	cleansedCtx, cancelShutdown := context.WithTimeout(ctx, 5*time.Second)
	defer cancelShutdown()

	if err := s.server.Shutdown(cleansedCtx); err != nil {
		log.Printf("Shutdown error: %v\n", err)
	} else {
		log.Printf("Shutdown successful\n")
	}
}
