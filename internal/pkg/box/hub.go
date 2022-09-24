package box

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/models"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

var broker = &Client{
	Id:    0,
	Xid:   xid.NilID().String(),
	Name:  "SERVER",
	conn:  nil,
	hub:   nil,
	rooms: nil,
	send:  nil,
}

type Hub struct {
	register   chan *Client
	unregister chan *Client

	users   []models.User
	clients map[*Client]bool
	rooms   map[*Room]bool

	roomStore models.RoomStore
	userStore models.UserStore
}

func NewHub(roomStore models.RoomStore, userStore models.UserStore) *Hub {
	hub := &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),

		clients: make(map[*Client]bool),
		rooms:   make(map[*Room]bool),

		roomStore: roomStore,
		userStore: userStore,
	}

	hub.users = userStore.GetAllUsers()

	return hub
}

func (hub *Hub) Run() {
	go hub.listenPubSubChannel()

	for {
		select {

		case client := <-hub.register:
			hub.addClient(client)

		case client := <-hub.unregister:
			hub.removeClient(client)
		}
	}
}

func (hub *Hub) addClient(client *Client) {
	hub.publishClientJoined(client)
	hub.clients[client] = true
	fmt.Println("Joined size of connection pool: ", len(hub.clients))
}

func (hub *Hub) removeClient(client *Client) {
	if _, ok := hub.clients[client]; ok {
		delete(hub.clients, client)
		hub.userStore.RemoveUser(client)
		hub.publishClientLeft(client)
		fmt.Println("Remaining size of connection pool: ", len(hub.clients))
	}
}

func (hub *Hub) broadcastToClients(message []byte) {
	for client := range hub.clients {
		client.send <- message
	}
}

func (hub *Hub) createRoom(client *Client, name string, private bool) *Room {
	room := &Room{
		Xid:         xid.New().String(),
		Name:        name,
		Description: "",
		Owner_Id:    client.GetXid(),
		Private:     private,
		clients:     make(map[*Client]bool),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		broadcast:   make(chan *Message),
	}

	hub.userStore.AddUser(client)
	hub.roomStore.AddRoom(room, client.Xid)

	go room.Run()
	hub.rooms[room] = true

	return room
}

func (hub *Hub) findClientById(ID string) *Client {
	var foundClient *Client
	for client := range hub.clients {
		if client.GetXid() == ID {
			foundClient = client
			break
		}
	}

	return foundClient
}

func (hub *Hub) findUserById(ID string) models.User {
	var foundUser models.User
	for _, user := range hub.users {
		if user.GetXid() == ID {
			foundUser = user
			break
		}
	}

	return foundUser
}

func (hub *Hub) findRoomByName(client *Client, name string) *Room {
	var foundRoom *Room
	for room := range hub.rooms {
		if room.GetName() == name {
			foundRoom = room
			break
		}
	}

	if foundRoom == nil {
		foundRoom = hub.runRoomFromStore(client, name)
	}

	return foundRoom
}

func (hub *Hub) findRoomByXid(xid string) *Room {
	var foundRoom *Room
	for room := range hub.rooms {
		if room.GetXid() == xid {
			foundRoom = room
			break
		}
	}

	return foundRoom
}

func (hub *Hub) runRoomFromStore(client *Client, name string) *Room {
	var room *Room
	dbRoom := hub.roomStore.FindRoomByName(name)
	// create room if it doesn't exist in roomStore
	if dbRoom == nil {
		room = hub.createRoom(client, name, false) // rooms are not private for now
	} else {
		// room exists, create room struct, run it, and add to rooms map
		room = &Room{
			Xid:         dbRoom.GetXid(),
			Name:        dbRoom.GetName(),
			Description: dbRoom.GetDescription(),
			Owner_Id:    dbRoom.GetOwnerId(),
			Private:     dbRoom.GetPrivate(),
			clients:     make(map[*Client]bool),
			register:    make(chan *Client),
			unregister:  make(chan *Client),
			broadcast:   make(chan *Message),
		}

		go room.Run()
		hub.rooms[room] = true
	}

	return room
}

func (hub *Hub) publishClientJoined(client *Client) {
	msg := &Message{
		Type:   string(message.Server),
		Action: string(message.JoinRoomMessage),
		Body:   fmt.Sprintf("%v has joined. Say hi.", client.GetXid()),
		Sender: broker,
	}

	if err := Redis.Publish(ctx, "general", msg.encode()).Err(); err != nil {
		log.Println(err)
	}
}

func (hub *Hub) publishClientLeft(client *Client) {
	msg := &Message{
		Type:   string(message.Server),
		Action: string(message.LeaveRoomMessage),
		Body:   fmt.Sprintf("%v left the room.", client.GetXid()),
		Sender: broker,
	}

	if err := Redis.Publish(ctx, "general", msg.encode()).Err(); err != nil {
		log.Println(err)
	}
}

func (hub *Hub) listenPubSubChannel() {
	pubsub := Redis.Subscribe(ctx, "general")

	ch := pubsub.Channel()

	for msg := range ch {
		var m Message
		if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
			log.Printf("Error on unmarshal JSON message %s", err)
			return
		}

		switch m.Action {
		case message.JoinRoom.String():
			hub.handleUserJoined(m)
		case message.LeaveRoom.String():
			hub.handleUserLeft(m)
		}
	}
}

func (hub *Hub) handleUserJoined(msg Message) {
	hub.users = append(hub.users, msg.Sender)
	hub.broadcastToClients(msg.encode())
}

func (hub *Hub) handleUserLeft(msg Message) {
	for i, user := range hub.users {
		if user.GetId() == msg.Sender.GetId() {
			hub.users[i] = hub.users[len(hub.users)-1]
			hub.users = hub.users[:len(hub.users)-1]
		}
	}

	hub.broadcastToClients(msg.encode())
}
