package box

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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

type simulationConfig struct {
	URL      string
	Users    int
	Rooms    int
	Messages int
	Interval time.Duration
	Timeout  time.Duration
}

func simulationConfigFromEnv() (simulationConfig, error) {
	cfg := simulationConfig{
		URL: strings.TrimSpace(os.Getenv("SIM_URL")), Users: 10, Rooms: 2,
		Messages: 10, Interval: 20 * time.Millisecond, Timeout: 15 * time.Second,
	}
	for _, setting := range []struct {
		name  string
		value *int
	}{
		{"SIM_USERS", &cfg.Users}, {"SIM_ROOMS", &cfg.Rooms}, {"SIM_MESSAGES", &cfg.Messages},
	} {
		if raw := strings.TrimSpace(os.Getenv(setting.name)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return cfg, fmt.Errorf("%s must be an integer: %w", setting.name, err)
			}
			*setting.value = value
		}
	}
	for _, setting := range []struct {
		name  string
		value *time.Duration
	}{
		{"SIM_INTERVAL", &cfg.Interval}, {"SIM_TIMEOUT", &cfg.Timeout},
	} {
		if raw := strings.TrimSpace(os.Getenv(setting.name)); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil {
				return cfg, fmt.Errorf("%s must be a duration such as 20ms or 15s: %w", setting.name, err)
			}
			*setting.value = value
		}
	}
	if cfg.Users < 2 || cfg.Rooms < 1 || cfg.Rooms > cfg.Users/2 {
		return cfg, fmt.Errorf("SIM_USERS must be at least 2 and SIM_ROOMS must be between 1 and SIM_USERS/2 (at least two users per room)")
	}
	if cfg.Messages < 1 || cfg.Interval < 0 || cfg.Timeout <= 0 {
		return cfg, fmt.Errorf("SIM_MESSAGES and SIM_TIMEOUT must be positive; SIM_INTERVAL must not be negative")
	}
	if cfg.URL != "" {
		address, err := url.Parse(cfg.URL)
		if err != nil || (address.Scheme != "ws" && address.Scheme != "wss") || address.Hostname() == "" || address.Fragment != "" {
			return cfg, fmt.Errorf("SIM_URL must be a ws:// or wss:// URL with a host and no fragment")
		}
	}
	return cfg, nil
}

// Use the wire format, rather than the server's connection state, to behave like
// real clients. A server frame can contain multiple newline-separated messages.
type simulationMessage struct {
	Type   string      `json:"type"`
	Action string      `json:"action,omitempty"`
	Room   *store.Room `json:"room,omitempty"`
	Body   string      `json:"body,omitempty"`
	Sender *store.User `json:"sender,omitempty"`
}

func decodeSimulationFrame(data []byte) ([]simulationMessage, error) {
	var messages []simulationMessage
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event simulationMessage
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("invalid server JSON: %w", err)
		}
		if event.Type != message.Server.String() && event.Type != message.Normal.String() {
			return nil, fmt.Errorf("unexpected server message type %q", event.Type)
		}
		messages = append(messages, event)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("empty server message")
	}
	return messages, nil
}

type simulationEvent struct {
	client  int
	message simulationMessage
	pong    string
	err     error
}

type simulationPeer struct {
	conn    *websocket.Conn
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	writeMu sync.Mutex
}

func (peer *simulationPeer) close() {
	peer.once.Do(func() {
		close(peer.stop)
		peer.conn.Close()
	})
	<-peer.done
}

type userSimulation struct {
	ctx        context.Context
	cfg        simulationConfig
	id         string
	peers      []*simulationPeer
	senderIDs  []string
	events     chan simulationEvent
	rooms      []*store.Room
	background func(simulationEvent) (bool, error)
}

func (sim *userSimulation) connect(index int) error {
	ctx, cancel := context.WithTimeout(sim.ctx, sim.cfg.Timeout)
	defer cancel()
	dialer := websocket.Dialer{HandshakeTimeout: sim.cfg.Timeout}
	conn, response, err := dialer.DialContext(ctx, sim.cfg.URL, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return fmt.Errorf("connect user %d: %w", index+1, err)
	}
	peer := &simulationPeer{conn: conn, stop: make(chan struct{}), done: make(chan struct{})}
	sim.peers[index] = peer
	conn.SetPongHandler(func(data string) error {
		select {
		case sim.events <- simulationEvent{client: index, pong: data}:
		case <-peer.stop:
		case <-sim.ctx.Done():
		}
		return nil
	})
	go func() {
		defer close(peer.done)
		for {
			_, data, err := conn.ReadMessage()
			var messages []simulationMessage
			if err == nil {
				messages, err = decodeSimulationFrame(data)
			}
			if err != nil {
				select {
				case <-peer.stop:
					return
				default:
				}
				select {
				case sim.events <- simulationEvent{client: index, err: err}:
				case <-peer.stop:
				case <-sim.ctx.Done():
				}
				return
			}
			for _, event := range messages {
				select {
				case sim.events <- simulationEvent{client: index, message: event}:
				case <-peer.stop:
					return
				case <-sim.ctx.Done():
					return
				}
			}
		}
	}()
	return nil
}

func (sim *userSimulation) close() {
	for _, peer := range sim.peers {
		if peer != nil {
			peer.close()
		}
	}
}

func (sim *userSimulation) write(ctx context.Context, index int, event simulationMessage) error {
	peer := sim.peers[index]
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if err := peer.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return peer.conn.WriteJSON(event)
}

// Writers and readers run together so the simulated clients keep draining their
// sockets even while other clients are sending. Only this loop owns assertions.
func (sim *userSimulation) phase(label string, want int, write func(context.Context, int) error, accept func(simulationEvent) (bool, error)) error {
	return sim.runPhase(label, want, write, accept, nil)
}

func (sim *userSimulation) runPhase(label string, want int, write func(context.Context, int) error, accept, acceptError func(simulationEvent) (bool, error)) error {
	ctx, cancel := context.WithTimeout(sim.ctx, sim.cfg.Timeout)
	done := make(chan struct{})
	defer func() {
		cancel()
		<-done
	}()
	written := make(chan error, 1)
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		errs := make([]error, len(sim.peers))
		for index := range sim.peers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := write(ctx, index); err != nil {
					errs[index] = fmt.Errorf("user %d: %w", index+1, err)
				}
			}()
		}
		wg.Wait()
		written <- errors.Join(errs...)
	}()
	remaining := want
	for remaining > 0 || written != nil {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %d of %d expected events missing (or writes unfinished): %w", label, remaining, want, ctx.Err())
		case err := <-written:
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			written = nil
		case event := <-sim.events:
			var matched bool
			var err error
			if event.err != nil {
				if acceptError == nil {
					return fmt.Errorf("%s: user %d disconnected or received invalid data: %w", label, event.client+1, event.err)
				}
				matched, err = acceptError(event)
			} else {
				if sim.background != nil {
					handled, err := sim.background(event)
					if err != nil {
						return fmt.Errorf("%s: background traffic: %w", label, err)
					}
					if handled {
						continue
					}
				}
				matched, err = accept(event)
			}
			if err != nil {
				return fmt.Errorf("%s: user %d: %w", label, event.client+1, err)
			}
			if matched {
				remaining--
			}
		}
	}
	return nil
}

func (sim *userSimulation) join(names map[int]string) (map[int]*store.Room, error) {
	if sim.rooms == nil {
		sim.rooms = make([]*store.Room, len(sim.peers))
	}
	notices, err := sim.joinNotices(names)
	if err != nil {
		return nil, err
	}
	joined := make(map[int]*store.Room)
	var received []simulationEvent
	err = sim.phase("join rooms", len(names)+len(notices.expected), func(ctx context.Context, index int) error {
		name, ok := names[index]
		if !ok {
			return nil
		}
		return sim.write(ctx, index, simulationMessage{
			Type: message.Command.String(), Action: message.JoinRoom.String(), Room: &store.Room{Name: name},
		})
	}, func(event simulationEvent) (bool, error) {
		msg := event.message
		if msg.Type != message.Server.String() {
			return false, fmt.Errorf("unexpected chat message while joining: %q", msg.Body)
		}
		if msg.Action != message.NotifyJoinRoomMessage.String() {
			if err := checkSimulationServerMessage(msg); err != nil {
				return false, err
			}
			received = append(received, event)
			return true, nil
		}
		name, expected := names[event.client]
		if !expected || joined[event.client] != nil || msg.Room == nil || msg.Room.Name != name || msg.Room.Xid == "" {
			return false, fmt.Errorf("invalid or duplicate room acknowledgement: %+v", msg.Room)
		}
		if err := checkSimulationServerMessage(msg); err != nil {
			return false, err
		}
		suffix := " joined " + name + "."
		identity := strings.TrimSuffix(msg.Body, suffix)
		if identity == msg.Body || identity == "" {
			return false, fmt.Errorf("invalid room acknowledgement body %q", msg.Body)
		}
		if id, err := xid.FromString(identity); err != nil || id == xid.NilID() {
			return false, fmt.Errorf("invalid room acknowledgement identity %q", identity)
		}
		if err := sim.recordIdentity(event.client, identity); err != nil {
			return false, err
		}
		if previous := notices.rooms[name]; previous != nil && previous.Xid != msg.Room.Xid {
			return false, fmt.Errorf("users joining %q received different room IDs", name)
		}
		joined[event.client] = msg.Room
		notices.rooms[name] = msg.Room
		return true, nil
	})
	if err != nil {
		return joined, err
	}
	if len(joined) != len(names) {
		return joined, fmt.Errorf("missing room acknowledgements")
	}
	for _, event := range received {
		if err := notices.accept(event); err != nil {
			return joined, err
		}
	}
	if err := notices.complete(); err != nil {
		return joined, err
	}
	for index, room := range joined {
		sim.rooms[index] = room
	}
	return joined, nil
}

func (sim *userSimulation) chat(room *store.Room, body string) simulationMessage {
	return simulationMessage{
		Type: message.Normal.String(), Action: "forged-action", Room: &store.Room{Xid: room.Xid}, Body: body,
		Sender: &store.User{Xid: "simulation-forged-sender"},
	}
}

func (sim *userSimulation) deliver(label string, count int, rooms []*store.Room, extra map[int][]simulationMessage) (int, error) {
	bodies := make([][]string, len(sim.peers))
	for index, room := range rooms {
		if room == nil {
			continue
		}
		for sequence := 0; sequence < count; sequence++ {
			bodies[index] = append(bodies[index], fmt.Sprintf("%s:%s:%d:%d", sim.id, label, index, sequence))
		}
	}
	return sim.deliverBodies(label, rooms, bodies, extra)
}

func (sim *userSimulation) deliverBodies(label string, rooms []*store.Room, bodies [][]string, extra map[int][]simulationMessage) (int, error) {
	type delivery struct {
		sender   int
		roomID   string
		sequence int
	}
	members := make(map[string]map[int]bool)
	for index, room := range rooms {
		if room == nil {
			continue // A connected observer that has left its room.
		}
		if members[room.Xid] == nil {
			members[room.Xid] = make(map[int]bool)
		}
		members[room.Xid][index] = true
	}
	expected := make(map[string]delivery)
	sends := make([][]simulationMessage, len(sim.peers))
	seen := make([]map[string]bool, len(sim.peers))
	next := make([][]int, len(sim.peers))
	want := 0
	for index, room := range rooms {
		seen[index] = make(map[string]bool)
		next[index] = make([]int, len(sim.peers))
		sends[index] = append(sends[index], extra[index]...)
		if room == nil {
			continue
		}
		for sequence, body := range bodies[index] {
			if _, exists := expected[body]; exists {
				return 0, fmt.Errorf("duplicate simulation body %q", body)
			}
			expected[body] = delivery{sender: index, roomID: room.Xid, sequence: sequence}
			sends[index] = append(sends[index], sim.chat(room, body))
			want += len(members[room.Xid])
		}
	}
	err := sim.phase(label, want, func(ctx context.Context, index int) error {
		for sequence, msg := range sends[index] {
			if sequence > 0 && sim.cfg.Interval > 0 {
				timer := time.NewTimer(sim.cfg.Interval)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
			}
			if err := sim.write(ctx, index, msg); err != nil {
				return err
			}
		}
		return nil
	}, func(event simulationEvent) (bool, error) {
		msg := event.message
		if msg.Type == message.Server.String() {
			return false, fmt.Errorf("unexpected server notification %q during chat", msg.Action)
		}
		item, ok := expected[msg.Body]
		if !ok || msg.Action != message.SendMessage.String() {
			return false, fmt.Errorf("unexpected/unauthorized chat message %q with action %q", msg.Body, msg.Action)
		}
		if msg.Room == nil || msg.Room.Xid != item.roomID || !members[item.roomID][event.client] {
			return false, fmt.Errorf("message %q leaked to the wrong room or client", msg.Body)
		}
		if seen[event.client][msg.Body] {
			return false, fmt.Errorf("duplicate delivery of %q", msg.Body)
		}
		if item.sequence != next[event.client][item.sender] {
			return false, fmt.Errorf("out-of-order delivery from user %d: got sequence %d, want %d", item.sender+1, item.sequence, next[event.client][item.sender])
		}
		if msg.Sender == nil || msg.Sender.Xid == "" || msg.Sender.Xid == "simulation-forged-sender" {
			return false, fmt.Errorf("missing or forged sender for %q", msg.Body)
		}
		if err := sim.recordIdentity(item.sender, msg.Sender.Xid); err != nil {
			return false, err
		}
		seen[event.client][msg.Body] = true
		next[event.client][item.sender]++
		return true, nil
	})
	return want, err
}

func (sim *userSimulation) recordIdentity(sender int, identity string) error {
	if known := sim.senderIDs[sender]; known != "" && known != identity {
		return fmt.Errorf("sender changed for user %d", sender+1)
	}
	for index, known := range sim.senderIDs {
		if index != sender && known == identity {
			return fmt.Errorf("users %d and %d share a sender ID", index+1, sender+1)
		}
	}
	sim.senderIDs[sender] = identity
	return nil
}

func checkSimulationRooms(original []*store.Room, joined map[int]*store.Room) error {
	for index, room := range joined {
		if room.Xid != original[index].Xid {
			return fmt.Errorf("user %d rejoined a different room ID: %s instead of %s", index+1, room.Xid, original[index].Xid)
		}
	}
	return nil
}

func (sim *userSimulation) quiet() error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return nil
		case <-sim.ctx.Done():
			return sim.ctx.Err()
		case event := <-sim.events:
			if event.err != nil {
				return fmt.Errorf("user %d disconnected: %w", event.client+1, event.err)
			}
			if sim.background != nil {
				handled, err := sim.background(event)
				if err != nil {
					return err
				}
				if handled {
					continue
				}
			}
			return fmt.Errorf("user %d received a late duplicate/unauthorized message or notification: %q (%s)", event.client+1, event.message.Body, event.message.Action)
		}
	}
}

func TestSimulatedUsers(t *testing.T) {
	started := time.Now()
	cfg, err := simulationConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	local := cfg.URL == ""
	if local {
		logs := testutil.CaptureLogs(t)
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("Local server logs:\n%s", logs.String())
			}
		})
		startup, stop := context.WithTimeout(ctx, cfg.Timeout)
		defer stop()
		db, err := database.Open(startup, database.Config{URL: ":memory:"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		bus := broker.NewMemory()
		t.Cleanup(func() { bus.Close() })
		cfg.URL, _ = testServer(t, ctx, db, bus)
		t.Log("Using an isolated in-memory SQLite/memory-broker server.")
	} else {
		t.Log("Using SIM_URL; test users and rooms will persist on that server.")
	}
	sim := &userSimulation{
		ctx: ctx, cfg: cfg, id: "simulation-" + xid.New().String(),
		peers: make([]*simulationPeer, cfg.Users), senderIDs: make([]string, cfg.Users), events: make(chan simulationEvent, 256),
	}
	t.Cleanup(sim.close)
	names := make(map[int]string)
	for index := range sim.peers {
		if err := sim.connect(index); err != nil {
			t.Fatal(err)
		}
		names[index] = fmt.Sprintf("%s-room-%d", sim.id, index%cfg.Rooms)
	}
	joined, err := sim.join(names)
	if err != nil {
		t.Fatal(err)
	}
	rooms := make([]*store.Room, cfg.Users)
	roomIDs := make(map[string]string)
	roomNames := make(map[string]string)
	for index, room := range joined {
		if id, ok := roomIDs[room.Name]; ok && id != room.Xid {
			t.Fatalf("users joining %q received different room IDs", room.Name)
		}
		if name, ok := roomNames[room.Xid]; ok && name != room.Name {
			t.Fatal("different rooms share a room ID")
		}
		roomIDs[room.Name], roomNames[room.Xid], rooms[index] = room.Xid, room.Name, room
	}
	t.Logf("PASS: %d users joined %d rooms; join notifications verified.", cfg.Users, cfg.Rooms)
	forbidden := make(map[int][]simulationMessage)
	if cfg.Rooms > 1 {
		for index := range sim.peers {
			forbidden[index] = []simulationMessage{sim.chat(rooms[(index+1)%cfg.Rooms], fmt.Sprintf("%s:non-member:%d", sim.id, index))}
		}
	}
	deliveries, err := sim.deliver("fan-out", cfg.Messages, rooms, forbidden)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PASS: %d concurrent chat sends; all %d deliveries verified, including sender echoes and per-sender ordering.", cfg.Users*cfg.Messages, deliveries)

	rejoined, err := sim.join(names)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSimulationRooms(rooms, rejoined); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.deliver("repeated-join", 2, rooms, nil); err != nil {
		t.Fatal(err)
	}
	for index := range sim.peers {
		if err := sim.leave(index, true); err != nil {
			t.Fatal(err)
		}
		rejoined, err := sim.join(map[int]string{index: names[index]})
		if err != nil {
			t.Fatal(err)
		}
		if err := checkSimulationRooms(rooms, rejoined); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("PASS: repeated joins/leaves are idempotent; membership notifications and leave/rejoin authorization verified.")

	if err := sim.leave(0, false); err != nil {
		t.Fatal(err)
	}
	leftRooms := append([]*store.Room(nil), rooms...)
	leftRooms[0] = nil
	if _, err := sim.deliver("leave-isolation", 1, leftRooms, map[int][]simulationMessage{
		0: {sim.chat(rooms[0], sim.id+":while-left")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sim.quiet(); err != nil {
		t.Fatal(err)
	}
	rejoined, err = sim.join(map[int]string{0: names[0]})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSimulationRooms(rooms, rejoined); err != nil {
		t.Fatal(err)
	}
	switched, err := sim.join(map[int]string{0: sim.id + "-switched"})
	if err != nil {
		t.Fatal(err)
	}
	switchRooms := append([]*store.Room(nil), rooms...)
	switchRooms[0] = switched[0]
	if switchRooms[0].Xid == rooms[0].Xid {
		t.Fatal("switching rooms kept the original room ID")
	}
	if _, err := sim.deliver("room-switch", 1, switchRooms, map[int][]simulationMessage{
		0: {sim.chat(rooms[0], sim.id+":after-switch")},
	}); err != nil {
		t.Fatal(err)
	}

	// Observe late duplicates and leaks while the original sockets still exist.
	if err := sim.quiet(); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.join(map[int]string{0: names[0]}); err != nil {
		t.Fatal(err)
	}
	if err := sim.churn(names[0]); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS: leave, room switching, disconnect, and reconnect under continuing traffic verified.")
	if err := sim.payloadBoundaries(); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS: Unicode, embedded newlines, the exact frame-size limit, and oversized-frame isolation verified.")
	previousIDs := append([]string(nil), sim.senderIDs...)
	for index := range sim.peers {
		if err := sim.disconnect(index); err != nil {
			t.Fatal(err)
		}
		if err := sim.connect(index); err != nil {
			t.Fatal(err)
		}
		sim.senderIDs[index] = ""
		// Keep a member in each room to observe unregister completion. Closing
		// the last socket alone does not fence the server's asynchronous cleanup.
		reconnected, err := sim.join(map[int]string{index: names[index]})
		if err != nil {
			t.Fatal(err)
		}
		if err := checkSimulationRooms(rooms, reconnected); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sim.deliver("reconnect", 1, rooms, nil); err != nil {
		t.Fatal(err)
	}
	for index, previous := range previousIDs {
		if sim.senderIDs[index] == previous {
			t.Fatalf("reconnected user %d did not receive a new connection identity", index+1)
		}
	}
	if err := sim.quiet(); err != nil {
		t.Fatal(err)
	}
	if local && !t.Run("backpressure-recovery", testSimulationBackpressureRecovery) {
		return
	}
	t.Log("PASS: sender spoofing rejected, rooms isolated, leave/rejoin, room switching, and reconnects verified.")
	t.Logf("Simulation completed in %s.", time.Since(started).Round(time.Millisecond))
}
