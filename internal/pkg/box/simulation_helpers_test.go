package box

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

func clearSimulationEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"SIM_URL", "SIM_USERS", "SIM_ROOMS", "SIM_MESSAGES", "SIM_INTERVAL", "SIM_TIMEOUT"} {
		t.Setenv(name, "")
	}
}

func TestSimulationConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		clearSimulationEnv(t)
		cfg, err := simulationConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.URL != "" || cfg.Users != 10 || cfg.Rooms != 2 || cfg.Messages != 10 || cfg.Interval != 20*time.Millisecond || cfg.Timeout != 15*time.Second {
			t.Fatalf("unexpected defaults: %+v", cfg)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		clearSimulationEnv(t)
		for name, value := range map[string]string{
			"SIM_URL": "wss://example.com/ws", "SIM_USERS": " 12 ", "SIM_ROOMS": "3",
			"SIM_MESSAGES": "25", "SIM_INTERVAL": "0s", "SIM_TIMEOUT": "30s",
		} {
			t.Setenv(name, value)
		}
		cfg, err := simulationConfigFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.URL != "wss://example.com/ws" || cfg.Users != 12 || cfg.Rooms != 3 || cfg.Messages != 25 || cfg.Interval != 0 || cfg.Timeout != 30*time.Second {
			t.Fatalf("overrides were not applied: %+v", cfg)
		}
	})
	for _, test := range []struct {
		name  string
		value string
	}{
		{"SIM_USERS", "many"}, {"SIM_USERS", "1"}, {"SIM_USERS", "-1"},
		{"SIM_ROOMS", "0"}, {"SIM_ROOMS", "6"}, {"SIM_ROOMS", "two"},
		{"SIM_MESSAGES", "0"}, {"SIM_MESSAGES", "ten"},
		{"SIM_INTERVAL", "fast"}, {"SIM_INTERVAL", "-1ms"},
		{"SIM_TIMEOUT", "0s"}, {"SIM_TIMEOUT", "-1s"}, {"SIM_TIMEOUT", "later"},
		{"SIM_URL", "http://localhost/ws"}, {"SIM_URL", "ws:///ws"},
		{"SIM_URL", "ws://localhost/ws#fragment"}, {"SIM_URL", ":invalid"},
	} {
		t.Run(test.name+"="+test.value, func(t *testing.T) {
			clearSimulationEnv(t)
			t.Setenv(test.name, test.value)
			if _, err := simulationConfigFromEnv(); err == nil {
				t.Fatal("expected invalid configuration to fail")
			}
		})
	}
}

func TestDecodeSimulationFrame(t *testing.T) {
	t.Run("batched messages", func(t *testing.T) {
		data := []byte(`{"type":"normal","action":"send-message","body":"one"}` + "\n" +
			`{"type":"normal","action":"send-message","body":"two"}` + "\n")
		messages, err := decodeSimulationFrame(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 2 || messages[0].Body != "one" || messages[1].Body != "two" {
			t.Fatalf("batched messages were lost or reordered: %+v", messages)
		}
	})
	t.Run("server notification", func(t *testing.T) {
		messages, err := decodeSimulationFrame([]byte(`{"type":"server","room":{"xid":"room-id"}}`))
		if err != nil || len(messages) != 1 || messages[0].Room == nil || messages[0].Room.Xid != "room-id" {
			t.Fatalf("unexpected notification: %+v, %v", messages, err)
		}
	})
	for _, data := range []string{"", " \n", "not-json", `{}`, `null`, `{"type":"unknown"}`, `{"type":"normal"}` + "\ninvalid"} {
		t.Run("invalid "+strings.ReplaceAll(data, "\n", "newline"), func(t *testing.T) {
			if _, err := decodeSimulationFrame([]byte(data)); err == nil {
				t.Fatal("expected invalid server data to fail")
			}
		})
	}
}

func TestSimulationDeliveryChecks(t *testing.T) {
	for _, test := range []struct {
		fault string
		want  string
	}{
		{"none", ""}, {"missing", "expected events missing"},
		{"duplicate", "duplicate"}, {"wrong room", "wrong room"},
		{"delayed duplicate", "late duplicate"},
		{"forged sender", "forged sender"}, {"shared sender", "share a sender ID"},
		{"wrong action", "with action"}, {"unexpected body", "unauthorized"},
		{"invalid JSON", "invalid server JSON"},
		{"reordered", "out-of-order"}, {"sender changed", "sender changed"},
		{"missing sender", "missing or forged sender"}, {"unexpected notification", "unexpected server notification"},
	} {
		t.Run(test.fault, func(t *testing.T) {
			var identities atomic.Int64
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				identity := fmt.Sprintf("sender-%d", identities.Add(1))
				var held []byte
				sequence := 0
				for {
					var reply simulationMessage
					if err := conn.ReadJSON(&reply); err != nil {
						return
					}
					reply.Action = message.SendMessage.String()
					reply.Sender = &store.User{Xid: identity}
					switch test.fault {
					case "missing":
						continue
					case "wrong room":
						reply.Room.Xid = "wrong-room"
					case "forged sender":
						reply.Sender.Xid = "simulation-forged-sender"
					case "shared sender":
						reply.Sender.Xid = "shared-sender"
					case "wrong action":
						reply.Action = "wrong-action"
					case "unexpected body":
						reply.Body = "unexpected-body"
					case "sender changed":
						if sequence > 0 {
							reply.Sender.Xid += "-changed"
						}
					case "missing sender":
						reply.Sender = nil
					case "unexpected notification":
						reply.Type = message.Server.String()
						reply.Action = "unexpected-notification"
					}
					sequence++
					data, err := json.Marshal(reply)
					if err != nil {
						return
					}
					if test.fault == "duplicate" {
						data = bytes.Join([][]byte{data, data}, []byte{'\n'})
					} else if test.fault == "invalid JSON" {
						data = []byte("not-json")
					} else if test.fault == "reordered" {
						if held == nil {
							held = data
							continue
						}
						data = bytes.Join([][]byte{data, held}, []byte{'\n'})
						held = nil
					}
					if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
						return
					}
					if test.fault == "delayed duplicate" {
						time.Sleep(50 * time.Millisecond)
						if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
							return
						}
					}
				}
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			sim := &userSimulation{
				ctx: ctx, id: "fault-test", cfg: simulationConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: time.Second},
				peers: make([]*simulationPeer, 2), senderIDs: make([]string, 2), events: make(chan simulationEvent, 16),
			}
			t.Cleanup(sim.close)
			for index := range sim.peers {
				if err := sim.connect(index); err != nil {
					t.Fatal(err)
				}
			}
			count := 1
			if test.fault == "reordered" || test.fault == "sender changed" {
				count = 2
			}
			_, err := sim.deliver("fault-test", count, []*store.Room{{Xid: "one"}, {Xid: "two"}}, nil)
			if err == nil {
				err = sim.quiet()
			}
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected failure containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestSimulationNotificationChecks(t *testing.T) {
	for _, test := range []struct {
		fault string
		want  string
	}{
		{"none", ""}, {"missing", "missing membership notification"},
		{"duplicate", "duplicate membership notification"},
		{"wrong room ID", "wrong room"}, {"wrong room name", "wrong room"},
		{"wrong recipient", "wrong recipient"}, {"self recipient", "wrong recipient"},
		{"wrong actor", "unexpected actor"}, {"wrong body", "body/actor"},
		{"wrong action", "notification action"}, {"unexpected acknowledgement", "membership notification action"},
		{"missing room", "invalid server notification"}, {"missing sender", "notification sender"},
		{"forged sender", "notification sender"}, {"wrong type", "invalid server notification"},
	} {
		t.Run(test.fault, func(t *testing.T) {
			room := &store.Room{Xid: "room-one", Name: "one"}
			sim := &userSimulation{
				senderIDs: []string{"user-one", "user-two", "observer"},
				rooms:     []*store.Room{room, room, {Xid: "room-two", Name: "two"}},
			}
			notices := sim.newNotices()
			notices.expectLeave(0)
			event := simulationEvent{client: 1, message: simulationMessage{
				Type: message.Server.String(), Action: message.LeaveRoomMessage.String(),
				Room: &store.Room{Xid: room.Xid, Name: room.Name}, Body: "user-one left the room.",
				Sender: &store.User{Xid: xid.NilID().String(), Name: "SERVER"},
			}}
			switch test.fault {
			case "wrong room ID":
				event.message.Room.Xid = "wrong-id"
			case "wrong room name":
				event.message.Room.Name = "wrong-name"
			case "wrong recipient":
				event.client = 2
			case "self recipient":
				event.client = 0
			case "wrong actor":
				event.message.Body = "user-two left the room."
			case "wrong body":
				event.message.Body = "invalid notification"
			case "wrong action":
				event.message.Action = "wrong-action"
			case "unexpected acknowledgement":
				event.message.Action = message.NotifyJoinRoomMessage.String()
			case "missing room":
				event.message.Room = nil
			case "missing sender":
				event.message.Sender = nil
			case "forged sender":
				event.message.Sender.Xid = "forged"
			case "wrong type":
				event.message.Type = message.Normal.String()
			}
			var err error
			if test.fault != "missing" {
				err = notices.accept(event)
			}
			if err == nil && test.fault == "duplicate" {
				err = notices.accept(event)
			}
			if err == nil {
				err = notices.complete()
			}
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected failure containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestSimulationConcurrentJoinNotifications(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint("reverse=", reverse), func(t *testing.T) {
			sim := &userSimulation{rooms: make([]*store.Room, 3), senderIDs: []string{"one", "two", "three"}}
			notices, err := sim.joinNotices(map[int]string{0: "room", 1: "room", 2: "room"})
			if err != nil {
				t.Fatal(err)
			}
			notices.rooms["room"] = &store.Room{Xid: "room-id", Name: "room"}
			for actor := 0; actor < 3; actor++ {
				for client := actor + 1; client < 3; client++ {
					sender, recipient := actor, client
					if reverse {
						sender, recipient = recipient, sender
					}
					event := simulationEvent{client: recipient, message: simulationMessage{
						Type: message.Server.String(), Action: message.JoinRoomMessage.String(),
						Room: notices.rooms["room"], Body: sim.senderIDs[sender] + " has joined. Say hi.",
						Sender: &store.User{Xid: xid.NilID().String(), Name: "SERVER"},
					}}
					if err := notices.accept(event); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := notices.complete(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSimulationSizedChat(t *testing.T) {
	sim := &userSimulation{rooms: []*store.Room{{Xid: "room-id"}}}
	for _, size := range []int{maxMessageSize, maxMessageSize + 1} {
		msg, err := sim.sizedChat(0, "Unicode 👋\n<>&", size)
		if err != nil {
			t.Fatal(err)
		}
		var frame bytes.Buffer
		if err := json.NewEncoder(&frame).Encode(msg); err != nil {
			t.Fatal(err)
		}
		if frame.Len() != size {
			t.Fatalf("encoded frame has %d bytes, want %d", frame.Len(), size)
		}
	}
	if _, err := sim.sizedChat(0, "body", 1); err == nil {
		t.Fatal("expected undersized frame request to fail")
	}
}

func TestSimulationTrafficChecks(t *testing.T) {
	for _, test := range []struct {
		fault string
		want  string
	}{
		{"none", ""}, {"transition delivery", ""},
		{"duplicate", "duplicate"}, {"reordered", "out-of-order"},
		{"room leak", "leaked"}, {"forged sender", "invalid churn traffic"},
		{"absent client", "outside its room"}, {"replay", "replayed"},
		{"actor duplicate", "duplicate"}, {"unsent", "unsent"},
	} {
		t.Run(test.fault, func(t *testing.T) {
			traffic := &simulationTraffic{
				room: &store.Room{Xid: "room-id"}, identity: "sender-id", prefix: "traffic:",
				members: map[int]bool{1: true}, next: make([]int, 3),
				lastActor: -1, total: -1, outsideStart: -1,
			}
			event := simulationEvent{client: 1, message: simulationMessage{
				Type: message.Normal.String(), Action: message.SendMessage.String(),
				Room: traffic.room, Body: "traffic:0", Sender: &store.User{Xid: traffic.identity},
			}}
			switch test.fault {
			case "transition delivery":
				event.client = 0
			case "reordered":
				event.message.Body = "traffic:1"
			case "room leak":
				event.client = 2
			case "forged sender":
				event.message.Sender.Xid = "forged"
			case "absent client":
				event.client = 0
				traffic.outsideStart = 0
			case "replay":
				event.client = 0
				traffic.forbidden = [][2]int{{0, 5}}
			case "actor duplicate":
				event.client = 0
				traffic.lastActor = 0
			case "unsent":
				traffic.total = 0
			}
			handled, err := traffic.accept(event)
			if !handled {
				t.Fatal("churn traffic was not recognized")
			}
			if err == nil && test.fault == "duplicate" {
				_, err = traffic.accept(event)
			}
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected failure containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestE2ESimulationExternalEndpoint(t *testing.T) {
	clearSimulationEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := database.Open(ctx, database.Config{URL: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bus := broker.NewMemory()
	t.Cleanup(func() { bus.Close() })
	address, _ := testServer(t, ctx, db, bus)
	for name, value := range map[string]string{
		"SIM_URL": address, "SIM_USERS": "2", "SIM_ROOMS": "1",
		"SIM_MESSAGES": "1", "SIM_INTERVAL": "0s", "SIM_TIMEOUT": "5s",
	} {
		t.Setenv(name, value)
	}
	// Exercise the existing-endpoint path without touching a user's deployment.
	t.Run("existing endpoint", TestSimulatedUsers)
}
