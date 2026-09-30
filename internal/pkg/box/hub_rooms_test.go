package box

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

func TestHubRoomCacheLifecycle(t *testing.T) {
	for _, action := range []string{"leave", "switch", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			var lookups atomic.Int64
			rooms := hubWorkerRooms{find: func(ctx context.Context, name string) (*store.Room, error) {
				if name == "room" {
					lookups.Add(1)
				}
				return &store.Room{Xid: "id-" + name, Name: name}, nil
			}}
			hub := newWorkerHub(t, broker.NewMemory(), rooms, hubWorkerUsers{})
			first, second := workerClient(t, hub, "first"), workerClient(t, hub, "second")
			room := workerJoin(t, first, "room")
			workerJoin(t, second, " room ")
			original := first.room
			remove := func(client *Client) {
				t.Helper()
				switch action {
				case "leave":
					awaitWorker(t, submitWorkerCommand(t, client, Message{
						Type: message.Command.String(), Action: message.LeaveRoom.String(), Room: room,
					}))
				case "switch":
					workerJoin(t, client, "other")
				case "disconnect":
					hub.unregister <- client
					awaitWorker(t, client.done)
				}
			}

			remove(first)
			workerJoin(t, second, "room")
			if lookups.Load() != 1 || second.room != original || len(original.clients) != 1 {
				t.Fatal("room with a remaining member was not retained")
			}
			remove(second)
			// All commands are complete, so the hub's room state is idle here.
			if hub.rooms[room.Xid] != nil || hub.roomsByName[room.Name] != nil {
				t.Fatal("empty room was retained in a cache index")
			}

			rejoining := workerClient(t, hub, "rejoining")
			if reloaded := workerJoin(t, rejoining, "\troom\n"); reloaded.Xid != room.Xid {
				t.Fatal("rejoining changed the persisted room ID")
			}
			hub.Close()
			if lookups.Load() != 2 || rejoining.room == original {
				t.Fatal("rejoining did not reload the evicted room")
			}
			if hub.rooms[room.Xid] != rejoining.room || hub.roomsByName[room.Name] != rejoining.room || len(rejoining.room.clients) != 1 {
				t.Fatal("reloaded room has inconsistent indexes or membership")
			}
		})
	}
}

func TestHubConcurrentRoomLookups(t *testing.T) {
	for _, evict := range []bool{false, true} {
		t.Run(fmt.Sprint("evict=", evict), func(t *testing.T) {
			started := make(chan chan struct{})
			rooms := hubWorkerRooms{find: func(ctx context.Context, name string) (*store.Room, error) {
				release := make(chan struct{})
				select {
				case started <- release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				select {
				case <-release:
					return &store.Room{Xid: "room-id", Name: name}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}}
			hub := newWorkerHub(t, broker.NewMemory(), rooms, hubWorkerUsers{})
			first, second := workerClient(t, hub, "first"), workerClient(t, hub, "second")
			awaitWorker(t, first.registered)
			awaitWorker(t, second.registered)
			join := Message{
				Type: message.Command.String(), Action: message.JoinRoom.String(),
				Room: &Room{Room: store.Room{Name: "room"}},
			}
			firstDone := submitWorkerCommand(t, first, join)
			firstRelease := <-started
			secondDone := submitWorkerCommand(t, second, join)
			secondRelease := <-started
			close(firstRelease)
			awaitWorker(t, firstDone)
			original := first.room
			if evict {
				awaitWorker(t, submitWorkerCommand(t, first, Message{
					Type: message.Command.String(), Action: message.LeaveRoom.String(), Room: original,
				}))
			}
			close(secondRelease)
			awaitWorker(t, secondDone)
			hub.Close()
			room := second.room
			if room == nil || hub.rooms[room.Xid] != room || hub.roomsByName[room.Name] != room {
				t.Fatal("concurrent lookup left inconsistent room indexes")
			}
			if evict {
				if room == original || len(room.clients) != 1 || !room.clients[second] {
					t.Fatal("pending lookup did not restore the evicted room")
				}
			} else if room != original || len(room.clients) != 2 || !room.clients[first] || !room.clients[second] {
				t.Fatal("concurrent lookups did not preserve both room members")
			}
		})
	}
}

func BenchmarkHubJoinCachedRoom(b *testing.B) {
	for _, count := range []int{1, 100, 10000} {
		b.Run(fmt.Sprintf("rooms=%d", count), func(b *testing.B) {
			hub := &Hub{
				rooms: make(map[string]*Room, count), roomsByName: make(map[string]*Room, count),
			}
			var client *Client
			for index := range count {
				name := fmt.Sprintf("room-%d", index)
				room := &Room{Room: store.Room{Xid: name, Name: name}}
				client = &Client{
					User: store.User{Xid: name}, room: room,
					send: make(chan []byte, 1), done: make(chan struct{}),
				}
				room.clients = map[*Client]bool{client: true}
				hub.rooms[room.Xid] = room
				hub.roomsByName[room.Name] = room
			}
			command := clientCommand{client: client, message: Message{
				Type: message.Command.String(), Action: message.JoinRoom.String(),
				Room: &Room{Room: store.Room{Name: client.room.Name}},
			}}
			b.ReportAllocs()
			for b.Loop() {
				if hub.handleCommand(command) {
					b.Fatal("cached room join queued a database lookup")
				}
				<-client.send
			}
		})
	}
}
