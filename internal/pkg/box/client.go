package box

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sonastea/chatterbox/internal/pkg/store"
)

var newLine = ([]byte{'\n'})

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1000
)

type Client struct {
	store.User
	conn *websocket.Conn

	hub   *Hub
	rooms map[*Room]bool

	send       chan []byte
	done       chan struct{}
	registered chan struct{}
	closeOnce  sync.Once
}

func (client *Client) close() {
	client.closeOnce.Do(func() {
		close(client.done)
		if client.conn != nil {
			client.conn.Close()
		}
	})
}

// A slow connection must not block the hub or other subscribers indefinitely.
func (client *Client) enqueue(msg []byte) {
	select {
	case <-client.done:
		return
	default:
	}
	select {
	case client.send <- msg:
	default:
		client.close()
	}
}

func (client *Client) readPump() {
	defer func() {
		client.close()
		select {
		case client.hub.unregister <- client:
		case <-client.hub.ctx.Done():
		}
	}()

	select {
	case <-client.registered:
	case <-client.done:
		return
	case <-client.hub.ctx.Done():
		return
	}

	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, message, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
				websocket.CloseNormalClosure) {
				slog.WarnContext(client.hub.ctx, "websocket read failed", "error", err, "client.id", client.Xid)
			}
			break
		}

		client.handleIncomingMessage(message)
	}
}

func (client *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.close()
	}()

	for {
		select {
		case <-client.done:
			return
		case msg := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))

			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}

			if _, err := w.Write(msg); err != nil {
				return
			}

			n := len(client.send)
			for i := 0; i < n; i++ {
				if _, err := w.Write(newLine); err != nil {
					return
				}
				if _, err := w.Write(<-client.send); err != nil {
					return
				}
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (client *Client) handleIncomingMessage(msg []byte) {
	var m Message
	if err := json.Unmarshal(msg, &m); err != nil {
		slog.WarnContext(client.hub.ctx, "invalid JSON message", "error", err, "client.id", client.Xid)
		return
	}
	m.Sender = client
	command := clientCommand{client: client, message: m, done: make(chan struct{})}
	select {
	case client.hub.commands <- command:
	case <-client.hub.ctx.Done():
		return
	case <-client.done:
		return
	}
	// One outstanding command per connection bounds work and preserves ordering
	// through asynchronous registration, room lookup, and publication.
	select {
	case <-command.done:
	case <-client.hub.ctx.Done():
	case <-client.done:
	}
}
