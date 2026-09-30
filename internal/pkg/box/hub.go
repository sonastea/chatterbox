package box

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	publish    chan publication
	database   chan databaseJob
	results    chan databaseResult
	workers    sync.WaitGroup

	// All client and room state is owned by the hub event loop.
	clients     map[*Client]bool // false while registration is pending
	rooms       map[string]*Room
	roomsByName map[string]*Room

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
	done    chan struct{}
}

func (command clientCommand) complete() {
	close(command.done)
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
		publish:      make(chan publication, hubWorkQueueSize),
		database:     make(chan databaseJob, hubWorkQueueSize),
		results:      make(chan databaseResult),
		clients:      make(map[*Client]bool),
		rooms:        make(map[string]*Room),
		roomsByName:  make(map[string]*Room),
		bus:          bus,
		subscription: subscription,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		roomStore:    roomStore,
		userStore:    userStore,
	}
	hub.workers.Go(hub.runPublisher)
	for range hubDatabaseWorkers {
		hub.workers.Go(hub.runDatabaseWorker)
	}
	go hub.run()
	return hub, nil
}

func (hub *Hub) run() {
	defer close(hub.done)
	defer hub.subscription.Close()
	defer hub.workers.Wait()
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
			if !hub.clients[command.client] || !hub.handleCommand(command) {
				command.complete()
			}
		case result := <-hub.results:
			hub.applyDatabaseResult(result)
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
	hub.clients[client] = false
	hub.queueDatabase(databaseJob{client: client, user: client.User})
}

func (hub *Hub) removeClient(client *Client) {
	hub.leaveRoom(client)
	delete(hub.clients, client)
	// Persist users after disconnect: room/message foreign keys still reference them.
	client.close()
}

// A true result transfers completion to a worker (or its result handler).
func (hub *Hub) handleCommand(command clientCommand) bool {
	client, msg := command.client, command.message
	select {
	case <-client.done:
		return false
	default:
	}
	if msg.Room == nil {
		return false
	}
	switch msg.Type {
	case message.Normal.String():
		room := hub.rooms[msg.Room.Xid]
		if room == nil || client.room != room {
			return false
		}
		msg.Room, msg.Sender, msg.Action = room, client, message.SendMessage.String()
		// Encode on the hub so workers only receive an immutable payload and IDs.
		job := publication{
			topic: "room." + room.Xid, payload: msg.encode(),
			clientID: client.Xid, roomID: room.Xid, done: command.done,
			deadline: time.Now().Add(hubWorkTimeout),
		}
		select {
		case hub.publish <- job:
			return true
		default:
			hub.overloaded(client, "publish")
		}
	case message.Command.String():
		switch msg.Action {
		case message.JoinRoom.String():
			name := strings.TrimSpace(msg.Room.Name)
			if name == "" {
				return false
			}
			if room := hub.roomsByName[name]; room != nil {
				hub.joinRoom(client, room)
				return false
			}
			return hub.queueDatabase(databaseJob{
				client: client, user: client.User, roomName: name, done: command.done,
			})
		case message.LeaveRoom.String():
			if room := hub.rooms[msg.Room.Xid]; room != nil && client.room == room {
				hub.leaveRoom(client)
			}
		}
	}
	return false
}

func (hub *Hub) joinRoom(client *Client, room *Room) {
	if client.room != room {
		hub.leaveRoom(client)
		client.room = room
		room.registerClientInRoom(client)
	}
	notification := Message{
		Type: message.Server.String(), Action: message.NotifyJoinRoomMessage.String(),
		Room: room, Body: fmt.Sprintf("%v joined %v.", client.Xid, room.Name), Sender: serverSender,
	}
	client.enqueue(notification.encode())
}

func (hub *Hub) leaveRoom(client *Client) {
	if room := client.room; room != nil {
		client.room = nil
		room.unregisterClientInRoom(client)
		if len(room.clients) == 0 {
			delete(hub.rooms, room.Xid)
			delete(hub.roomsByName, room.Name)
		}
	}
}

func (hub *Hub) overloaded(client *Client, queue string) {
	slog.WarnContext(hub.ctx, "hub work queue full; disconnecting client", "queue", queue, "client.id", client.Xid)
	hub.removeClient(client)
}
