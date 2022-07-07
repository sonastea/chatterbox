package box

import (
	"fmt"

	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

type Room struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Private bool `json:"private"`
	clients map[*Client]bool

	register   chan *Client
	unregister chan *Client

	broadcast chan Message
}

func (room *Room) GetID() string {
	return room.ID
}

func (room *Room) GetName() string {
	return room.Name
}

func (room *Room) Run() {
	for {
		select {
		case client := <-room.register:
			room.registerClientInRoom(client)

		case client := <-room.unregister:
			room.unregisterClientInRoom(client)

		case message := <-room.broadcast:
			room.broadcastToClientsInRoom(message)
		}
	}
}

func (room *Room) registerClientInRoom(client *Client) {
	msg := Message{
		Type:   string(message.Server),
		Action: string(message.JoinRoomMessage),
		Room:   room,
		Body:   fmt.Sprintf("%v has joined. Say hi.", client.GetID()),
		Sender: broker,
	}

	room.broadcastToClientsInRoom(msg)
	room.clients[client] = true
}

func (room *Room) unregisterClientInRoom(client *Client) {
	msg := Message{
		Type:   string(message.Server),
		Action: string(message.LeaveRoomMessage),
		Room:   room,
		Body:   fmt.Sprintf("%v left the room.", client.GetID()),
		Sender: broker,
	}

	if _, ok := room.clients[client]; ok {
		delete(room.clients, client)
	}

	room.broadcastToClientsInRoom(msg)
}

func (room *Room) broadcastToClientsInRoom(msg Message) {
	for client := range room.clients {
		client.send <- msg
	}
}
