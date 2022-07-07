package box

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Returning true for now, but should check origin.
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

type Client struct {
	sync.RWMutex
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	conn *websocket.Conn

	hub   *Hub
	rooms map[*Room]bool

	send chan Message
}

func (client *Client) GetID() string {
	return client.ID
}

func (client *Client) readPump() {
	defer func() {
		client.hub.unregister <- client
		for room := range client.rooms {
			room.unregister <- client
		}
		client.conn.Close()
		close(client.send)
	}()

	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		message := &Message{Sender: client, Room: &Room{ID: "0"}}
		err := client.conn.ReadJSON(message)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}

		client.handleIncomingMessage(*message)
	}
}

func (client *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			client.conn.WriteJSON(message)

		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &Client{
		ID:    xid.New().String(),
		hub:   hub,
		conn:  conn,
		rooms: make(map[*Room]bool),
		send:  make(chan Message),
	}

	client.hub.register <- client

	go client.writePump()
	go client.readPump()
}

func (client *Client) handleIncomingMessage(msg Message) {
	switch msg.Type {
	case message.Normal.String():
		client.handleSendMessage(msg)

	case message.Command.String():
		switch msg.Action {
		case message.JoinRoom.String():
			client.handleJoinRoom(msg)
		case message.LeaveRoom.String():
			client.handleLeaveRoom(msg)
		}
	}
}

func (client *Client) handleSendMessage(msg Message) {
	msg.Action = message.SendMessage.String()
	roomID := msg.Room.GetID()

	if room := client.hub.findRoomByID(roomID); room != nil {
		msg.Sender = client
		room.broadcast <- msg
	}
}

func (client *Client) handleJoinRoom(msg Message) {
	roomName := msg.Room.GetName()
	room := client.hub.findRoomByName(roomName)
	if room == nil {
		room = client.hub.createRoom(roomName, false)
	}

	if !client.isInRoom(room) {
		client.rooms[room] = true
		room.register <- client
		client.notifyRoomJoined(room, client)
	}
}

func (client *Client) handleLeaveRoom(msg Message) {
	room := client.hub.findRoomByID(msg.Room.ID)
	if room == nil {
		return
	}

	if _, ok := client.rooms[room]; ok {
		delete(client.rooms, room)
	}

	room.unregister <- client
}

func (client *Client) isInRoom(room *Room) bool {
	if _, ok := client.rooms[room]; ok {
		return true
	}

	return false
}

func (client *Client) notifyRoomJoined(room *Room, sender *Client) {
	message := Message{
		Type:   string(message.Server),
		Action: string(message.NotifyJoinRoomMessage),
		Room:   room,
		Body:   fmt.Sprintf("%v joined %v", sender.GetID(), room.GetName()),
		Sender: broker,
	}

	client.send <- message
}
