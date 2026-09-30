package box

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

var serverSender = &Client{
	User: store.User{Xid: xid.NilID().String(), Name: "SERVER"},
}

type Hub struct {
	register   chan *Client
	unregister chan *Client
	commands   chan clientCommand

	// All client and room state is owned by the hub event loop.
	clients map[*Client]bool
	rooms   map[string]*Room

	bus          broker.Broker
	subscription broker.Subscription
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}

	roomStore store.RoomRepository
	userStore store.UserRepository
}

type clientCommand struct {
	client  *Client
	message Message
}

// NewHub starts a hub using injected repositories and a broker. The caller owns
// the broker and repositories; closing this hub only closes its subscription.
func NewHub(ctx context.Context, bus broker.Broker, roomStore store.RoomRepository, userStore store.UserRepository) (*Hub, error) {
	ctx, cancel := context.WithCancel(ctx)
	subscription, err := bus.Subscribe(ctx, "room.*")
	if err != nil {
		cancel()
		return nil, err
	}
	hub := &Hub{
		register:     make(chan *Client),
		unregister:   make(chan *Client),
		commands:     make(chan clientCommand),
		clients:      make(map[*Client]bool),
		rooms:        make(map[string]*Room),
		bus:          bus,
		subscription: subscription,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		roomStore:    roomStore,
		userStore:    userStore,
	}
	go hub.run()
	return hub, nil
}

func (hub *Hub) run() {
	defer close(hub.done)
	defer hub.subscription.Close()
	defer hub.cancel()
	defer func() {
		for client := range hub.clients {
			client.close()
		}
	}()
	for {
		select {
		case <-hub.ctx.Done():
			return
		case client := <-hub.register:
			hub.addClient(client)
		case client := <-hub.unregister:
			hub.removeClient(client)
		case command := <-hub.commands:
			if hub.clients[command.client] {
				hub.handleCommand(command)
			}
		case msg, ok := <-hub.subscription.Messages():
			if !ok {
				if hub.ctx.Err() == nil {
					slog.ErrorContext(hub.ctx, "broker subscription closed; stopping hub", "topic", "room.*")
				}
				return
			}
			if room := hub.rooms[strings.TrimPrefix(msg.Topic, "room.")]; room != nil {
				room.broadcastToClientsInRoom(msg.Payload)
			}
		}
	}
}

func (hub *Hub) Close() error {
	hub.cancel()
	<-hub.done
	return hub.subscription.Close()
}

func (hub *Hub) addClient(client *Client) {
	ctx, cancel := context.WithTimeout(hub.ctx, 5*time.Second)
	defer cancel()
	if _, err := hub.userStore.AddUser(ctx, client.User); err != nil {
		slog.ErrorContext(ctx, "register client failed", "error", err, "client.id", client.Xid)
		client.close()
		return
	}
	hub.clients[client] = true
}

func (hub *Hub) removeClient(client *Client) {
	for room := range client.rooms {
		room.unregisterClientInRoom(client)
		delete(client.rooms, room)
	}
	delete(hub.clients, client)
	// Persist users after disconnect: room/message foreign keys still reference them.
	client.close()
}

func (hub *Hub) handleCommand(command clientCommand) {
	client, msg := command.client, command.message
	if msg.Room == nil {
		return
	}
	switch msg.Type {
	case message.Normal.String():
		room := hub.rooms[msg.Room.Xid]
		if room == nil || !client.rooms[room] {
			return
		}
		msg.Room, msg.Sender, msg.Action = room, client, message.SendMessage.String()
		ctx, cancel := context.WithTimeout(hub.ctx, 5*time.Second)
		defer cancel()
		if err := hub.bus.Publish(ctx, "room."+room.Xid, msg.encode()); err != nil {
			slog.ErrorContext(ctx, "publish room message failed", "error", err,
				"client.id", client.Xid, "room.id", room.Xid)
		}
	case message.Command.String():
		switch msg.Action {
		case message.JoinRoom.String():
			hub.joinRoom(client, msg.Room.Name)
		case message.LeaveRoom.String():
			if room := hub.rooms[msg.Room.Xid]; room != nil && client.rooms[room] {
				delete(client.rooms, room)
				room.unregisterClientInRoom(client)
			}
		}
	}
}

func (hub *Hub) joinRoom(client *Client, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	room, err := hub.findRoomByName(&client.User, name)
	if err != nil {
		slog.ErrorContext(hub.ctx, "join room failed", "error", err, "client.id", client.Xid, "room.name", name)
		return
	}
	if !client.rooms[room] {
		for previous := range client.rooms {
			delete(client.rooms, previous)
			previous.unregisterClientInRoom(client)
		}
		client.rooms[room] = true
		room.registerClientInRoom(client)
	}
	notification := Message{
		Type: message.Server.String(), Action: message.NotifyJoinRoomMessage.String(),
		Room: room, Body: fmt.Sprintf("%v joined %v.", client.Xid, room.Name), Sender: serverSender,
	}
	client.enqueue(notification.encode())
}

func (hub *Hub) findRoomByName(client *store.User, name string) (*Room, error) {
	for _, room := range hub.rooms {
		if room.Name == name {
			return room, nil
		}
	}
	ctx, cancel := context.WithTimeout(hub.ctx, 5*time.Second)
	defer cancel()
	dbRoom, err := hub.roomStore.FindRoomByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		dbRoom, err = hub.roomStore.AddRoom(ctx, store.Room{Xid: xid.New().String(), Name: name, Owner_ID: client.Xid})
		if err != nil {
			// Another server may have created the same room concurrently.
			if existing, findErr := hub.roomStore.FindRoomByName(ctx, name); findErr == nil {
				dbRoom, err = existing, nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	room := &Room{Room: *dbRoom, clients: make(map[*Client]bool)}
	hub.rooms[room.Xid] = room
	return room, nil
}
