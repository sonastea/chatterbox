package websocket

import (
	"fmt"
	"net/http"
)

type Pool struct {
	Join      chan *Client
	Leave     chan *Client
	Clients   map[*Client]bool
	Broadcast chan Message
}

func NewPool() *Pool {
	return &Pool{
		Join:      make(chan *Client),
		Leave:     make(chan *Client),
		Clients:   make(map[*Client]bool),
		Broadcast: make(chan Message),
	}
}

func ServeWs(pool *Pool, w http.ResponseWriter, r *http.Request) {
	fmt.Println("Websocket established")
	conn, err := Upgrade(w, r)
	if err != nil {
		fmt.Fprintf(w, "%+s\n", err)
	}

	client := &Client{
		Conn: conn,
		Pool: pool,
	}

	pool.Join <- client
	client.Read()
}

func (pool *Pool) Start() {
	for {
		select {
		case client := <-pool.Join:
			pool.Clients[client] = true
			fmt.Println("Joined size of connection pool: ", len(pool.Clients))
			for client := range pool.Clients {
				// fmt.Println(&client)
				client.Conn.WriteJSON(Message{Type: 1, Body: "User joined..."})
			}
			break
		case client := <-pool.Leave:
			delete(pool.Clients, client)
			fmt.Println("Remaining size of connection pool: ", len(pool.Clients))
			for client := range pool.Clients {
				client.Conn.WriteJSON(Message{Type: 1, Body: "User left..."})
			}
			break
		case message := <-pool.Broadcast:
			fmt.Println("Sending message to all clients in pool")
			for client := range pool.Clients {
				if err := client.Conn.WriteJSON(message); err != nil {
					fmt.Println(err)
					return
				}
			}
		}
	}
}
