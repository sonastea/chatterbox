package box

import (
	"fmt"

	"github.com/rs/xid"
)

var broker = &Client{
	ID:    xid.NilID().String(),
	Name:  "SERVER",
	conn:  nil,
	hub:   nil,
	rooms: nil,
	send:  nil,
}

type Hub struct {
	register   chan *Client
	unregister chan *Client

	clients map[*Client]bool
	rooms   map[*Room]bool

	broadcast chan Message
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),

		clients: make(map[*Client]bool),
		rooms:   make(map[*Room]bool),

		broadcast: make(chan Message),
	}
}

func (hub *Hub) Run() {
	for {
		select {

		case client := <-hub.register:
			hub.addClient(client)

		case client := <-hub.unregister:
			hub.removeClient(client)

		case message := <-hub.broadcast:
			hub.broadcastToClients(message)
		}
	}
}

func (hub *Hub) addClient(client *Client) {
	hub.clients[client] = true
	fmt.Println("Joined size of connection pool: ", len(hub.clients))
}

func (hub *Hub) removeClient(client *Client) {
	if _, ok := hub.clients[client]; ok {
		delete(hub.clients, client)
		fmt.Println("Remaining size of connection pool: ", len(hub.clients))
	}
}

func (hub *Hub) broadcastToClients(message Message) {
	for client := range hub.clients {
		client.send <- message
	}
}

func (hub *Hub) createRoom(name string, private bool) *Room {
	room := &Room{
		ID:         xid.New().String(),
		Name:       name,
		Private:    false,
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Message),
	}

	go room.Run()
	hub.rooms[room] = true

	return room
}

func (hub *Hub) findClientByID(ID string) *Client {
	var foundClient *Client
	for client := range hub.clients {
		if client.GetID() == ID {
			foundClient = client
			break
		}
	}

	return foundClient
}

func (hub *Hub) findRoomByName(name string) *Room {
	var foundRoom *Room
	for room := range hub.rooms {
		if room.GetName() == name {
			foundRoom = room
			break
		}
	}

	return foundRoom
}

func (hub *Hub) findRoomByID(ID string) *Room {
	var foundRoom *Room
	for room := range hub.rooms {
		if room.GetID() == ID {
			foundRoom = room
			break
		}
	}

	return foundRoom
}
