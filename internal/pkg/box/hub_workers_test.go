package box

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

type hubWorkerUsers struct {
	store.UserRepository
	add func(context.Context, store.User) (*store.User, error)
}

func (users hubWorkerUsers) AddUser(ctx context.Context, user store.User) (*store.User, error) {
	if users.add != nil {
		return users.add(ctx, user)
	}
	return &user, nil
}

type hubWorkerRooms struct {
	store.RoomRepository
	find func(context.Context, string) (*store.Room, error)
}

func (rooms hubWorkerRooms) FindRoomByName(ctx context.Context, name string) (*store.Room, error) {
	if rooms.find != nil {
		return rooms.find(ctx, name)
	}
	return &store.Room{Xid: name, Name: name}, nil
}

func newWorkerHub(t *testing.T, bus broker.Broker, rooms hubWorkerRooms, users hubWorkerUsers) *Hub {
	t.Helper()
	t.Cleanup(func() { bus.Close() })
	hub, err := NewHub(context.Background(), bus, rooms, users)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hub.Close() })
	return hub
}

func workerClient(t *testing.T, hub *Hub, id string) *Client {
	t.Helper()
	client := &Client{
		User: store.User{Xid: id}, hub: hub,
		send: make(chan []byte, 2*hubWorkQueueSize+16),
		done: make(chan struct{}), registered: make(chan struct{}),
	}
	select {
	case hub.register <- client:
	case <-time.After(time.Second):
		t.Fatal("hub stopped accepting registrations")
	}
	return client
}

func awaitWorker(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hub or worker did not make progress")
	}
}

func submitWorkerCommand(t *testing.T, client *Client, msg Message) <-chan struct{} {
	t.Helper()
	command := clientCommand{client: client, message: msg, done: make(chan struct{})}
	select {
	case client.hub.commands <- command:
	case <-time.After(time.Second):
		t.Fatal("hub stopped accepting commands")
	}
	return command.done
}

func workerJoin(t *testing.T, client *Client, name string) *Room {
	t.Helper()
	awaitWorker(t, client.registered)
	awaitWorker(t, submitWorkerCommand(t, client, Message{
		Type: message.Command.String(), Action: message.JoinRoom.String(), Room: &Room{Room: store.Room{Name: name}},
	}))
	return workerEvent(t, client, message.NotifyJoinRoomMessage.String()).Room
}

func workerEvent(t *testing.T, client *Client, action string) Message {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case payload := <-client.send:
			var msg Message
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Action == action {
				return msg
			}
		case <-deadline:
			t.Fatalf("missing %s event", action)
		}
	}
}

type hubBlockedPublisher struct {
	broker.Broker
	started chan struct{}
	release chan struct{}
}

func (bus *hubBlockedPublisher) Publish(ctx context.Context, topic string, payload []byte) error {
	select {
	case bus.started <- struct{}{}:
	default:
	}
	select {
	case <-bus.release:
		return bus.Broker.Publish(ctx, topic, payload)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestHubPublicationOrderingAndOverload(t *testing.T) {
	bus := &hubBlockedPublisher{Broker: broker.NewMemory(), started: make(chan struct{}, 1), release: make(chan struct{})}
	hub := newWorkerHub(t, bus, hubWorkerRooms{}, hubWorkerUsers{})
	sender := workerClient(t, hub, "sender")
	room := workerJoin(t, sender, "room")
	observer := workerClient(t, hub, "observer")
	workerJoin(t, observer, "room")

	// Exercise admission directly, independently of read-pump backpressure.
	var accepted []<-chan struct{}
	for sequence := range hubWorkQueueSize + 1 {
		accepted = append(accepted, submitWorkerCommand(t, sender, Message{
			Type: message.Normal.String(), Room: room, Body: fmt.Sprint(sequence),
		}))
		if sequence == 0 {
			awaitWorker(t, bus.started)
		}
	}
	awaitWorker(t, submitWorkerCommand(t, sender, Message{Type: message.Normal.String(), Room: room, Body: "overload"}))
	awaitWorker(t, sender.done)

	// An incoming broker event and another client's join still progress while
	// the publisher and its entire bounded queue are blocked.
	probe := Message{Type: message.Normal.String(), Action: message.SendMessage.String(), Body: "probe"}
	if err := bus.Broker.Publish(context.Background(), "room.room", probe.encode()); err != nil {
		t.Fatal(err)
	}
	if got := workerEvent(t, observer, message.SendMessage.String()); got.Body != "probe" {
		t.Fatalf("unexpected probe: %+v", got)
	}
	workerJoin(t, observer, "room")
	close(bus.release)
	for sequence, done := range accepted {
		awaitWorker(t, done)
		if got := workerEvent(t, observer, message.SendMessage.String()); got.Body != fmt.Sprint(sequence) {
			t.Fatalf("publication %d lost or reordered: %+v", sequence, got)
		}
	}
	// A fence also proves that the overloaded command was rejected.
	awaitWorker(t, submitWorkerCommand(t, observer, Message{Type: message.Normal.String(), Room: room, Body: "fence"}))
	if got := workerEvent(t, observer, message.SendMessage.String()); got.Body != "fence" {
		t.Fatalf("overloaded publication reached broker: %+v", got)
	}
}

func TestHubDatabaseOverloadAndShutdown(t *testing.T) {
	started, stopped := make(chan struct{}, hubDatabaseWorkers), make(chan struct{}, hubDatabaseWorkers)
	users := hubWorkerUsers{add: func(ctx context.Context, user store.User) (*store.User, error) {
		if user.Xid == "observer" {
			return &user, nil
		}
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return nil, ctx.Err()
	}}
	bus := broker.NewMemory()
	hub := newWorkerHub(t, bus, hubWorkerRooms{}, users)
	observer := workerClient(t, hub, "observer")
	workerJoin(t, observer, "room")
	var pending []*Client
	for index := range hubDatabaseWorkers + hubWorkQueueSize {
		pending = append(pending, workerClient(t, hub, fmt.Sprint(index)))
		if index < hubDatabaseWorkers {
			awaitWorker(t, started)
		}
	}
	overloaded := workerClient(t, hub, "overloaded")
	awaitWorker(t, overloaded.done)
	workerJoin(t, observer, "room") // cached rooms bypass saturated database workers
	probe := Message{Type: message.Normal.String(), Action: message.SendMessage.String(), Body: "probe"}
	if err := bus.Publish(context.Background(), "room.room", probe.encode()); err != nil {
		t.Fatal(err)
	}
	workerEvent(t, observer, message.SendMessage.String())

	closed := make(chan struct{})
	go func() { hub.Close(); close(closed) }()
	awaitWorker(t, closed)
	for range hubDatabaseWorkers {
		awaitWorker(t, stopped)
	}
	for _, client := range pending {
		awaitWorker(t, client.done)
		select {
		case <-client.registered:
			t.Fatal("pending registration became active during shutdown")
		default:
		}
	}
}

func TestHubRoomLookupOrderingAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprint("disconnect=", disconnect), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			rooms := hubWorkerRooms{find: func(ctx context.Context, name string) (*store.Room, error) {
				if name == "slow" {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return &store.Room{Xid: name, Name: name}, nil
			}}
			hub := newWorkerHub(t, broker.NewMemory(), rooms, hubWorkerUsers{})
			actor, observer := workerClient(t, hub, "actor"), workerClient(t, hub, "observer")
			old := workerJoin(t, actor, "old")
			workerJoin(t, observer, "old")
			slow := &Room{Room: store.Room{Xid: "slow", Name: "slow"}}
			command := clientCommand{client: actor, done: make(chan struct{}), message: Message{
				Type: message.Command.String(), Action: message.JoinRoom.String(), Room: slow,
			}}
			if disconnect {
				command.done = make(chan struct{})
				hub.commands <- command
				awaitWorker(t, started)
				hub.unregister <- actor
				awaitWorker(t, actor.done)
				close(release)
				awaitWorker(t, command.done) // result applied after unregister
				hub.Close()
				if _, exists := hub.clients[actor]; exists || actor.room != nil || hub.rooms["slow"] != nil {
					t.Fatal("late room result resurrected a disconnected client")
				}
				return
			}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				for _, msg := range []Message{
					command.message,
					{Type: message.Normal.String(), Room: old, Body: "unauthorized old room"},
					{Type: message.Command.String(), Action: message.LeaveRoom.String(), Room: slow},
					{Type: message.Normal.String(), Room: slow, Body: "unauthorized left room"},
					{Type: message.Command.String(), Action: message.JoinRoom.String(), Room: old},
					{Type: message.Normal.String(), Room: old, Body: "fence"},
				} {
					actor.handleIncomingMessage(msg.encode())
				}
			}()
			awaitWorker(t, started)
			awaitWorker(t, submitWorkerCommand(t, observer, Message{Type: message.Normal.String(), Room: old, Body: "probe"}))
			if got := workerEvent(t, observer, message.SendMessage.String()); got.Body != "probe" {
				t.Fatalf("blocked lookup allowed later commands to overtake: %+v", got)
			}
			close(release)
			awaitWorker(t, finished)
			if got := workerEvent(t, observer, message.SendMessage.String()); got.Body != "fence" {
				t.Fatalf("commands reordered around room lookup: %+v", got)
			}
			hub.Close()
			if actor.room == nil || actor.room != hub.rooms["old"] {
				t.Fatal("room commands did not preserve final membership")
			}
		})
	}
}

func TestHubShutdownCancelsPublisher(t *testing.T) {
	bus := &hubBlockedPublisher{Broker: broker.NewMemory(), started: make(chan struct{}, 1), release: make(chan struct{})}
	hub := newWorkerHub(t, bus, hubWorkerRooms{}, hubWorkerUsers{})
	client := workerClient(t, hub, "client")
	room := workerJoin(t, client, "room")
	done := submitWorkerCommand(t, client, Message{Type: message.Normal.String(), Room: room})
	awaitWorker(t, bus.started)
	closed := make(chan struct{})
	go func() { hub.Close(); close(closed) }()
	awaitWorker(t, closed)
	awaitWorker(t, done)
	awaitWorker(t, client.done)
}
