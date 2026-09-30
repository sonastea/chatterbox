package box

import (
	"fmt"

	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

type Room struct {
	store.Room
	clients map[*Client]bool
}

func (room *Room) registerClientInRoom(client *Client) {
	msg := Message{
		Type:   string(message.Server),
		Action: string(message.JoinRoomMessage),
		Room:   room,
		Body:   fmt.Sprintf("%v has joined. Say hi.", client.GetXid()),
		Sender: serverSender,
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
		Sender: serverSender,
	}

	if _, ok := room.clients[client]; ok {
		delete(room.clients, client)
	}

	room.broadcastToClientsInRoom(msg.encode())
}

func (room *Room) broadcastToClientsInRoom(msg []byte) {
	for client := range room.clients {
		client.enqueue(msg)
	}
}
