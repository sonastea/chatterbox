package box

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/store"
)

const (
	hubWorkQueueSize   = 256
	hubDatabaseWorkers = 4
	hubWorkTimeout     = 5 * time.Second
)

type publication struct {
	topic    string
	payload  []byte
	clientID string
	roomID   string
	done     chan struct{}
	deadline time.Time
}

// Exactly one publisher preserves the hub's acceptance order across rooms.
// Waiting happens here and in each sender's read pump, never in the hub loop.
func (hub *Hub) runPublisher() {
	for hub.ctx.Err() == nil {
		select {
		case <-hub.ctx.Done():
			return
		case job := <-hub.publish:
			ctx, cancel := context.WithDeadline(hub.ctx, job.deadline)
			err := ctx.Err()
			if err == nil {
				err = hub.bus.Publish(ctx, job.topic, job.payload)
			}
			if err != nil {
				slog.ErrorContext(ctx, "publish room message failed", "error", err,
					"client.id", job.clientID, "room.id", job.roomID)
			}
			cancel()
			close(job.done)
		}
	}
}

// An empty roomName denotes registration. Workers use value snapshots; only
// applyDatabaseResult may change hub membership or the room cache.
type databaseJob struct {
	client   *Client
	user     store.User
	roomName string
	done     chan struct{}
	deadline time.Time
}

type databaseResult struct {
	job  databaseJob
	room *store.Room
	err  error
}

func (hub *Hub) queueDatabase(job databaseJob) bool {
	job.deadline = time.Now().Add(hubWorkTimeout)
	select {
	case hub.database <- job:
		return true
	default:
		hub.overloaded(job.client, "database")
		return false
	}
}

func (hub *Hub) runDatabaseWorker() {
	for hub.ctx.Err() == nil {
		select {
		case <-hub.ctx.Done():
			return
		case job := <-hub.database:
			// Disconnected clients need neither registration nor a room lookup.
			select {
			case <-job.client.done:
				continue
			default:
			}
			ctx, cancel := context.WithDeadline(hub.ctx, job.deadline)
			result := databaseResult{job: job, err: ctx.Err()}
			if result.err == nil {
				if job.roomName == "" {
					_, result.err = hub.userStore.AddUser(ctx, job.user)
				} else {
					result.room, result.err = hub.loadRoom(ctx, job.user.Xid, job.roomName)
				}
			}
			cancel()
			select {
			case hub.results <- result:
			case <-hub.ctx.Done():
				return
			}
		}
	}
}

func (hub *Hub) loadRoom(ctx context.Context, ownerID, name string) (*store.Room, error) {
	room, err := hub.roomStore.FindRoomByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		room, err = hub.roomStore.AddRoom(ctx, store.Room{Xid: xid.New().String(), Name: name, Owner_ID: ownerID})
		if err != nil {
			// Another worker or server may have created the same room concurrently.
			if existing, findErr := hub.roomStore.FindRoomByName(ctx, name); findErr == nil {
				room, err = existing, nil
			}
		}
	}
	return room, err
}

func (hub *Hub) applyDatabaseResult(result databaseResult) {
	job, client := result.job, result.job.client
	if job.done != nil {
		defer close(job.done)
	}
	if _, exists := hub.clients[client]; !exists {
		return
	}
	select {
	case <-client.done:
		hub.removeClient(client)
		return
	default:
	}
	if job.roomName == "" {
		if result.err != nil {
			slog.ErrorContext(hub.ctx, "register client failed", "error", result.err, "client.id", client.Xid)
			hub.removeClient(client)
			return
		}
		hub.clients[client] = true
		close(client.registered)
		return
	}
	if result.err != nil {
		slog.ErrorContext(hub.ctx, "join room failed", "error", result.err, "client.id", client.Xid, "room.name", job.roomName)
		return
	}
	// Concurrent lookups must share one room object, preserving existing members.
	room := hub.rooms[result.room.Xid]
	if room == nil {
		room = &Room{Room: *result.room, clients: make(map[*Client]bool)}
		hub.rooms[room.Xid] = room
	}
	hub.joinRoom(client, room)
}
