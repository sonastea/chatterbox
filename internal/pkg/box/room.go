package box

import (
	"context"
	"fmt"
	"log"

	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

var ctx = context.Background()

type Room struct {
	Id          int    `json:"id"`
	Xid         string `json:"xid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Owner_Id    string `json:"owner_id"`

	Private bool `json:"private"`
	clients map[*Client]bool

	register   chan *Client
	unregister chan *Client

	broadcast chan *Message
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
	go room.subscribeToRoomMessages()

	for {
		select {
		case client := <-room.register:
			room.registerClientInRoom(client)

		case client := <-room.unregister:
			room.unregisterClientInRoom(client)

		case message := <-room.broadcast:
			room.publishRoomMessage(message.encode())
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

func (room *Room) publishRoomMessage(msg []byte) {
	err := Redis.Publish(ctx, room.GetName(), msg).Err()
	if err != nil {
		log.Println(err)
	}
}

func (room *Room) subscribeToRoomMessages() {
	pubsub := Redis.Subscribe(ctx, room.GetName())

	ch := pubsub.Channel()

	for msg := range ch {
		room.broadcastToClientsInRoom([]byte(msg.Payload))
	}
}

func (room *Room) notifyClientJoined(client *Client) {
	msg := &Message{
		Action: message.JoinRoomMessage.String(),
		Room:   room,
		Body:   fmt.Sprintf("%v joined the room. Say hi.", client.GetId()),
		Sender: broker,
	}

	room.publishRoomMessage(msg.encode())
}
