package box

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/internal/testutil"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

func TestE2EWebSocketFanout(t *testing.T) {
	for _, driver := range []string{"memory", "redis", "valkey", "nats", "rabbitmq"} {
		t.Run(driver, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, err := database.Open(ctx, database.Config{URL: ":memory:"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			var firstBus, secondBus broker.Broker
			if driver == "memory" {
				firstBus = broker.NewMemory()
				secondBus = firstBus
			} else {
				address := os.Getenv("TEST_" + strings.ToUpper(driver) + "_URL")
				if address == "" {
					t.Skip("use go run ./tests/e2e for external brokers")
				}
				firstBus, err = broker.Open(ctx, broker.Config{Driver: driver, URL: address})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { firstBus.Close() })
				secondBus, err = broker.Open(ctx, broker.Config{Driver: driver, URL: address})
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { firstBus.Close(); secondBus.Close() })
			first, _ := testServer(t, ctx, db, firstBus)
			second, _ := testServer(t, ctx, db, secondBus)
			one, two := connect(t, first), connect(t, second)
			name := "test-" + xid.New().String()
			// Submit both joins before waiting so room creation can race across hubs.
			for _, conn := range []*websocket.Conn{one, two} {
				writeMessage(t, conn, Message{Type: message.Command.String(), Action: message.JoinRoom.String(), Room: &Room{Room: store.Room{Name: name}}})
			}
			room := readEvent(t, one, message.NotifyJoinRoomMessage.String()).Room
			other := readEvent(t, two, message.NotifyJoinRoomMessage.String()).Room
			if room.Xid != other.Xid {
				t.Fatalf("same room on separate hubs has different ids: %s and %s", room.Xid, other.Xid)
			}

			// Inactive-room events used to dereference a nil room on other instances.
			if err := firstBus.Publish(ctx, "room."+xid.New().String(), []byte("ignored")); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{"not-json", `{}`, `{"type":"normal","room":null}`} {
				if err := one.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
					t.Fatal(err)
				}
			}
			writeMessage(t, one, Message{Type: message.Normal.String(), Room: room, Body: "cross-hub message"})
			for _, conn := range []*websocket.Conn{one, two} {
				event := readEvent(t, conn, message.SendMessage.String())
				if event.Body != "cross-hub message" || event.Room.Xid != room.Xid || event.Sender.Xid == "" {
					t.Fatalf("unexpected message: %+v", event)
				}
			}

			// Switching rooms removes old membership, including publish authorization.
			otherRoom := join(t, one, name+"-other")
			writeMessage(t, one, Message{Type: message.Normal.String(), Room: room, Body: "not-a-member"})
			writeMessage(t, one, Message{Type: message.Normal.String(), Room: otherRoom, Body: "new-room"})
			if event := readEvent(t, one, message.SendMessage.String()); event.Body != "new-room" {
				t.Fatalf("unexpected switched-room message: %+v", event)
			}
			writeMessage(t, two, Message{Type: message.Normal.String(), Room: room, Body: "original-room"})
			if event := readEvent(t, two, message.SendMessage.String()); event.Body != "original-room" {
				t.Fatalf("non-member published to original room: %+v", event)
			}

			// Leave and rejoin must use the same persisted record.
			writeMessage(t, one, Message{Type: message.Command.String(), Action: message.LeaveRoom.String(), Room: otherRoom})
			if rejoined := join(t, one, name); rejoined.Xid != room.Xid {
				t.Fatal("rejoining recreated the room")
			}
		})
	}
}

func TestE2EShutdownClosesWebSocketsAndKeepsRooms(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := database.Open(ctx, database.Config{URL: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bus := broker.NewMemory()
	t.Cleanup(func() { bus.Close() })
	url, server := testServer(t, ctx, db, bus)
	conn := connect(t, url)
	room := join(t, conn, "persistent")
	cancel()
	select {
	case <-server.hub.done:
	case <-time.After(5 * time.Second):
		t.Fatal("hub did not shut down")
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("shutdown left websocket open")
	}
	stored, err := (&store.RoomStore{DB: db}).FindRoomByXid(context.Background(), room.Xid)
	if err != nil || stored.Owner_ID == "" {
		t.Fatalf("room owner did not survive shutdown: %+v, %v", stored, err)
	}
	// A restarted hub can load and use the room without depending on its owner connection.
	restartedURL, _ := testServer(t, context.Background(), db, bus)
	restarted := connect(t, restartedURL)
	if loaded := join(t, restarted, "persistent"); loaded.Xid != room.Xid {
		t.Fatal("restart recreated existing room")
	}
}

func TestE2EStartHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db, err := database.Open(ctx, database.Config{URL: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bus := broker.NewMemory()
	defer bus.Close()
	server, err := NewServer(ctx, &Config{Addr: "127.0.0.1:0"}, bus, &store.RoomStore{DB: db}, &store.UserStore{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStartLogsTransport(t *testing.T) {
	for _, test := range []struct {
		name      string
		cert      string
		key       string
		transport string
	}{
		{
			name:      "http",
			transport: "HTTP/WS, app-level TLS disabled",
		},
		{
			name:      "https",
			cert:      "cert.pem",
			key:       "key.pem",
			transport: "HTTPS/WSS, TLS terminated by app",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := testutil.CaptureLogs(t)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			bus := broker.NewMemory()
			defer bus.Close()
			server, err := NewServer(ctx, &Config{Addr: "127.0.0.1:0", TLSCert: test.cert, TLSKey: test.key}, bus, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			// Verify the configured mode without opening a listener or loading TLS files.
			if err := server.server.Close(); err != nil {
				t.Fatal(err)
			}
			if err := server.Start(ctx); err != nil {
				t.Fatal(err)
			}
			_, record := testutil.DecodeLog(t, output.Bytes())
			if record.Body.StringValue != "chatterbox is now listening" || record.SeverityNumber != 9 {
				t.Fatalf("unexpected startup log: %+v", record)
			}
			attrs := record.AttributeMap()
			if attrs["address"]["stringValue"] != "127.0.0.1:0" || attrs["transport"]["stringValue"] != test.transport || attrs["tls.enabled"]["boolValue"] != (test.cert != "") {
				t.Fatalf("unexpected startup attributes: %+v", attrs)
			}
		})
	}
}

func TestErrorsUseOTLPJSON(t *testing.T) {
	t.Run("websocket upgrade", func(t *testing.T) {
		output := testutil.CaptureLogs(t)
		serveWs(nil, httptest.NewRecorder(), httptest.NewRequest("GET", "/ws", nil))
		_, record := testutil.DecodeLog(t, output.Bytes())
		if record.SeverityNumber != 13 || record.Body.StringValue != "websocket upgrade failed" || record.AttributeMap()["error"]["stringValue"] == "" {
			t.Fatalf("unexpected upgrade error log: %+v", record)
		}
	})
	t.Run("invalid message", func(t *testing.T) {
		output := testutil.CaptureLogs(t)
		client := &Client{User: store.User{Xid: "client-id"}, hub: &Hub{ctx: context.Background()}}
		client.handleIncomingMessage([]byte("not-json"))
		_, record := testutil.DecodeLog(t, output.Bytes())
		if record.SeverityNumber != 13 || record.Body.StringValue != "invalid JSON message" || record.AttributeMap()["client.id"]["stringValue"] != "client-id" {
			t.Fatalf("unexpected invalid-message log: %+v", record)
		}
	})
	t.Run("http diagnostics", func(t *testing.T) {
		output := testutil.CaptureLogs(t)
		bus := broker.NewMemory()
		defer bus.Close()
		server, err := NewServer(context.Background(), &Config{}, bus, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		server.server.ErrorLog.Print("http: test error")
		_, record := testutil.DecodeLog(t, output.Bytes())
		if record.SeverityNumber != 17 || record.Body.StringValue != "http: test error" || record.AttributeMap()["component"]["stringValue"] != "http" {
			t.Fatalf("unexpected HTTP diagnostic log: %+v", record)
		}
	})
}

func TestSlowClientDoesNotBlockRoom(t *testing.T) {
	slow := &Client{send: make(chan []byte, 1), done: make(chan struct{})}
	fast := &Client{send: make(chan []byte, 4), done: make(chan struct{})}
	room := &Room{clients: map[*Client]bool{slow: true, fast: true}}
	room.broadcastToClientsInRoom([]byte("first"))
	room.broadcastToClientsInRoom([]byte("second"))
	select {
	case <-slow.done:
	default:
		t.Fatal("slow client was not disconnected")
	}
	if got := string(<-fast.send); got != "first" {
		t.Fatal(got)
	}
	if got := string(<-fast.send); got != "second" {
		t.Fatal(got)
	}
}

func testServer(t testing.TB, ctx context.Context, db *database.DB, bus broker.Broker) (string, *Server) {
	t.Helper()
	server, err := NewServer(ctx, &Config{}, bus, &store.RoomStore{DB: db}, &store.UserStore{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	http := httptest.NewServer(server.server.Handler)
	t.Cleanup(func() { server.Close(); http.Close() })
	return "ws" + strings.TrimPrefix(http.URL, "http") + "/ws", server
}

func connect(t testing.TB, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func writeMessage(t testing.TB, conn *websocket.Conn, msg Message) {
	t.Helper()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatal(err)
	}
}

func join(t testing.TB, conn *websocket.Conn, name string) *Room {
	t.Helper()
	writeMessage(t, conn, Message{Type: message.Command.String(), Action: message.JoinRoom.String(), Room: &Room{Room: store.Room{Name: name}}})
	event := readEvent(t, conn, message.NotifyJoinRoomMessage.String())
	if event.Room == nil || event.Room.Xid == "" {
		t.Fatal("join returned no room")
	}
	return event.Room
}

func readEvent(t testing.TB, conn *websocket.Conn, action string) Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(fmt.Errorf("read %s event: %w", action, err))
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			var event Message
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatalf("invalid server message %q: %v", line, err)
			}
			if event.Action == action {
				return event
			}
		}
	}
}
