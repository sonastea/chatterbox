package box

import (
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/models"
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

	users     []models.User
	clients   map[*Client]bool
	rooms     map[*Room]bool
	roomsLive map[string]*Room

	pubsub *PubSub

	roomStore models.RoomStore
	userStore models.UserStore
}

func NewHub(redisOpt *redis.Options, roomStore models.RoomStore, userStore models.UserStore) (*Hub, error) {
	pubsub, err := newPubSub(redisOpt)
	if err != nil {
		return nil, err
	}

	hub := &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),

		clients:   make(map[*Client]bool),
		rooms:     make(map[*Room]bool),
		roomsLive: make(map[string]*Room),

		pubsub: pubsub,

		roomStore: roomStore,
		userStore: userStore,
	}

	hub.users = userStore.GetAllUsers()

	return hub, nil
}

func (hub *Hub) Run() {
	go hub.listenPubSub()

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
	hub.clients[client] = true
	fmt.Println("Joined size of connection pool: ", len(hub.clients))
}

func (hub *Hub) removeClient(client *Client) {
	if _, ok := hub.clients[client]; ok {
		delete(hub.clients, client)
		hub.userStore.RemoveUser(client)
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
		hub:         hub,
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		broadcast:   make(chan []byte),
	}

	hub.userStore.AddUser(client)
	hub.roomStore.AddRoom(room, client.Xid)

	return room
}

func (hub *Hub) sendToRoom(XID string, msg string) {
	hub.roomsLive[XID].broadcast <- []byte(msg)
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
			hub:         hub,
			clients:     make(map[*Client]bool),
			register:    make(chan *Client),
			unregister:  make(chan *Client),
			broadcast:   make(chan []byte),
		}
	}

	go room.Run()
	hub.rooms[room] = true
	hub.roomsLive[room.GetXid()] = room

	return room
}
