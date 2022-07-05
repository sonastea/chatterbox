package box

import (
	"fmt"

	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

type Hub struct {
	register   chan *Client
	unregister chan *Client
	clients    map[*Client]bool
	broadcast  chan Message
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		broadcast:  make(chan Message),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			for client := range h.clients {
				client.conn.WriteJSON(
					Message{
						Type:   message.Server.String(),
						Body:   fmt.Sprintf("%v joined...", client.ID.String()),
						Sender: "SERVER",
					})
			}
			fmt.Println("Joined size of connection pool: ", len(h.clients))
			break
		case client := <-h.unregister:
			for c := range h.clients {
				c.conn.WriteJSON(
					Message{
						Type: message.Server.String(),
						Body: fmt.Sprintf("%v left...", client.ID.String()),
						Sender: "SERVER",
					})
			}
			delete(h.clients, client)
			fmt.Println("Remaining size of connection pool: ", len(h.clients))
			break
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}
