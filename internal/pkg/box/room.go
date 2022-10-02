package box

import (
	"fmt"

	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

type Room struct {
	Id          int    `json:"id,omitempty"`
	Xid         string `json:"xid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Owner_Id    string `json:"owner_id,omitempty"`

	Private bool `json:"private"`
	clients map[*Client]bool

	hub *Hub

	register   chan *Client
	unregister chan *Client

	broadcast chan []byte
}

func (room *Room) GetId() int {
	return room.Id
}

func (room *Room) GetXid() string {
	return room.Xid
}

func (room *Room) GetPrivate() bool {
	return room.Private
}

func (room *Room) GetName() string {
	return room.Name
}

func (room *Room) GetDescription() string {
	return room.Description
}

func (room *Room) GetOwnerId() string {
	return room.Owner_Id
}

func (room *Room) Run() {
	for {
		select {
		case client := <-room.register:
			room.registerClientInRoom(client)

		case client := <-room.unregister:
			room.unregisterClientInRoom(client)

		case message := <-room.broadcast:
			room.broadcastToClientsInRoom([]byte(message))
		}
	}
}

func (room *Room) registerClientInRoom(client *Client) {
	msg := Message{
		Type:   string(message.Server),
		Action: string(message.JoinRoomMessage),
		Room:   room,
		Body:   fmt.Sprintf("%v has joined. Say hi.", client.GetXid()),
		Sender: broker,
	}

	room.broadcastToClientsInRoom(msg.encode())
	room.clients[client] = true
}

func (room *Room) unregisterClientInRoom(client *Client) {
	msg := Message{
		Type:   string(message.Server),
		Action: string(message.LeaveRoomMessage),
		Room:   room,
		Body:   fmt.Sprintf("%v left the room.", client.GetXid()),
		Sender: broker,
	}

	if _, ok := room.clients[client]; ok {
		delete(room.clients, client)
	}

	room.broadcastToClientsInRoom(msg.encode())
}

func (room *Room) broadcastToClientsInRoom(msg []byte) {
	for client := range room.clients {
		client.send <- msg
	}
}
