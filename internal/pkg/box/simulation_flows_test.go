package box

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

func simulationNoWrite(context.Context, int) error { return nil }

type simulationTraffic struct {
	sim          *userSimulation
	sender       int
	room         *store.Room
	identity     string
	prefix       string
	members      map[int]bool
	next         []int
	lastActor    int
	total        int
	outsideStart int
	forbidden    [][2]int
	sent         atomic.Int64
	stop         chan struct{}
	done         chan struct{}
	result       chan error
	once         sync.Once
	cancel       context.CancelFunc
}

func (sim *userSimulation) startTraffic() *simulationTraffic {
	ctx, cancel := context.WithCancel(sim.ctx)
	traffic := &simulationTraffic{
		sim: sim, room: sim.rooms[0], prefix: sim.id + ":churn-stream:",
		members: make(map[int]bool), next: make([]int, len(sim.peers)), lastActor: -1, total: -1, outsideStart: -1,
		stop: make(chan struct{}), done: make(chan struct{}), result: make(chan error, 1), cancel: cancel,
	}
	for client, room := range sim.rooms {
		if client != 0 && room != nil && room.Xid == traffic.room.Xid {
			traffic.members[client] = true
			traffic.sender = client
		}
	}
	traffic.identity = sim.senderIDs[traffic.sender]
	go func() {
		defer close(traffic.done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for sequence := 0; ; sequence++ {
			select {
			case <-ctx.Done():
				traffic.result <- ctx.Err()
				return
			case <-traffic.stop:
				traffic.result <- nil
				return
			default:
			}
			writeCtx, stop := context.WithTimeout(ctx, sim.cfg.Timeout)
			err := sim.write(writeCtx, traffic.sender, sim.chat(traffic.room, traffic.prefix+strconv.Itoa(sequence)))
			stop()
			if err != nil {
				traffic.result <- err
				select {
				case sim.events <- simulationEvent{client: traffic.sender, err: err}:
				case <-ctx.Done():
				}
				return
			}
			traffic.sent.Add(1)
			select {
			case <-ctx.Done():
				traffic.result <- ctx.Err()
				return
			case <-traffic.stop:
				traffic.result <- nil
				return
			case <-ticker.C:
			}
		}
	}()
	return traffic
}

func (traffic *simulationTraffic) stopSending() {
	traffic.once.Do(func() { close(traffic.stop) })
}

func (traffic *simulationTraffic) accept(event simulationEvent) (bool, error) {
	msg := event.message
	if msg.Type != message.Normal.String() || !strings.HasPrefix(msg.Body, traffic.prefix) {
		return false, nil
	}
	sequence, err := strconv.Atoi(strings.TrimPrefix(msg.Body, traffic.prefix))
	if err != nil || sequence < 0 || msg.Body != traffic.prefix+strconv.Itoa(sequence) {
		return true, fmt.Errorf("invalid churn traffic body %q", msg.Body)
	}
	if traffic.total >= 0 && sequence >= traffic.total {
		return true, fmt.Errorf("received unsent churn traffic sequence %d", sequence)
	}
	if msg.Action != message.SendMessage.String() || msg.Room == nil || msg.Room.Xid != traffic.room.Xid || msg.Sender == nil || msg.Sender.Xid != traffic.identity {
		return true, fmt.Errorf("invalid churn traffic action, room, or sender: %+v", msg)
	}
	if event.client == 0 {
		// Membership transitions can overlap an already-enqueued delivery. The
		// transition stream may have gaps for this client, but never duplicates
		// or reordering. Separate, barrier-ordered probes check isolation below.
		if traffic.outsideStart >= 0 && sequence >= traffic.outsideStart {
			return true, fmt.Errorf("churn traffic reached a client outside its room")
		}
		for _, interval := range traffic.forbidden {
			if sequence >= interval[0] && sequence <= interval[1] {
				return true, fmt.Errorf("churn traffic from an absent membership was delivered or replayed")
			}
		}
		if sequence <= traffic.lastActor {
			return true, fmt.Errorf("duplicate or out-of-order churn delivery to changing client")
		}
		traffic.lastActor = sequence
		return true, nil
	}
	if !traffic.members[event.client] {
		return true, fmt.Errorf("churn traffic leaked to user %d in another room", event.client+1)
	}
	if sequence != traffic.next[event.client] {
		return true, fmt.Errorf("missing, duplicate, or out-of-order churn delivery to user %d: got %d, want %d", event.client+1, sequence, traffic.next[event.client])
	}
	traffic.next[event.client]++
	return true, nil
}

func (traffic *simulationTraffic) beginOutside() {
	traffic.outsideStart = int(traffic.sent.Load()) + 1
}

func (traffic *simulationTraffic) endOutside() {
	// A stable sender echo fences every earlier publication on that socket.
	// Remember these sequences so a reconnect cannot replay absent traffic.
	traffic.forbidden = append(traffic.forbidden, [2]int{traffic.outsideStart, traffic.next[traffic.sender] - 1})
	traffic.outsideStart = -1
}

func (traffic *simulationTraffic) progress(label string) error {
	sim := traffic.sim
	// Skip any send already in flight, so the observed echo proves traffic
	// continued after this membership transition rather than merely queued up.
	target := int(traffic.sent.Load()) + 1
	sim.background = nil
	defer func() { sim.background = traffic.accept }()
	return sim.phase(label, 1, simulationNoWrite, func(event simulationEvent) (bool, error) {
		handled, err := traffic.accept(event)
		if !handled && err == nil {
			return false, fmt.Errorf("unexpected event while waiting for churn traffic: %+v", event)
		}
		return event.client == traffic.sender && traffic.next[traffic.sender] > target, err
	})
}

func (traffic *simulationTraffic) finish() error {
	sim := traffic.sim
	err := sim.phase("stop churn traffic", 0, func(ctx context.Context, client int) error {
		if client != traffic.sender {
			return nil
		}
		traffic.stopSending()
		select {
		case err := <-traffic.result:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}, func(event simulationEvent) (bool, error) {
		return false, fmt.Errorf("unexpected event while stopping traffic: %+v", event)
	})
	if err != nil {
		return err
	}
	sent := int(traffic.sent.Load())
	traffic.total = sent
	remaining := 0
	for client := range traffic.members {
		if traffic.next[client] > sent {
			return fmt.Errorf("user %d received unsent churn traffic", client+1)
		}
		remaining += sent - traffic.next[client]
	}
	sim.background = nil
	err = sim.phase("drain churn traffic", remaining, simulationNoWrite, func(event simulationEvent) (bool, error) {
		handled, err := traffic.accept(event)
		if !handled && err == nil {
			return false, fmt.Errorf("unexpected event while draining traffic: %+v", event)
		}
		return traffic.members[event.client], err
	})
	if err != nil {
		return err
	}
	for client := range traffic.members {
		if traffic.next[client] != sent {
			return fmt.Errorf("user %d received %d churn messages, want %d", client+1, traffic.next[client], sent)
		}
	}
	if traffic.lastActor >= sent {
		return fmt.Errorf("changing client received unsent churn traffic")
	}
	sim.background = traffic.accept
	return nil
}

func (sim *userSimulation) churn(name string) error {
	original := append([]*store.Room(nil), sim.rooms...)
	previousID := sim.senderIDs[0]
	traffic := sim.startTraffic()
	sim.background = traffic.accept
	defer func() {
		traffic.stopSending()
		traffic.cancel()
		<-traffic.done
		sim.background = nil
	}()
	if err := traffic.progress("start churn traffic"); err != nil {
		return err
	}
	if err := sim.leave(0, false); err != nil {
		return err
	}
	traffic.beginOutside()
	if _, err := sim.deliver("churn-left", 1, sim.rooms, map[int][]simulationMessage{
		0: {sim.chat(original[0], sim.id+":churn-left-forbidden")},
	}); err != nil {
		return err
	}
	if err := traffic.progress("traffic continues after leave"); err != nil {
		return err
	}
	if err := sim.quiet(); err != nil {
		return err
	}
	traffic.endOutside()
	if _, err := sim.join(map[int]string{0: name}); err != nil {
		return err
	}
	switched, err := sim.join(map[int]string{0: sim.id + "-churn-switched"})
	if err != nil {
		return err
	}
	if switched[0].Xid == original[0].Xid {
		return fmt.Errorf("churn room switch kept the original room ID")
	}
	traffic.beginOutside()
	if _, err := sim.deliver("churn-switched", 1, sim.rooms, map[int][]simulationMessage{
		0: {sim.chat(original[0], sim.id+":churn-switch-forbidden")},
	}); err != nil {
		return err
	}
	if err := traffic.progress("traffic continues after switch"); err != nil {
		return err
	}
	if err := sim.quiet(); err != nil {
		return err
	}
	traffic.endOutside()
	if _, err := sim.join(map[int]string{0: name}); err != nil {
		return err
	}
	if err := sim.disconnect(0); err != nil {
		return err
	}
	traffic.beginOutside()
	if _, err := sim.deliver("churn-disconnected", 1, sim.rooms, nil); err != nil {
		return err
	}
	if err := traffic.progress("traffic continues during disconnect"); err != nil {
		return err
	}
	if err := sim.quiet(); err != nil {
		return err
	}
	if err := sim.connect(0); err != nil {
		return err
	}
	sim.senderIDs[0] = ""
	traffic.endOutside()
	joined, err := sim.join(map[int]string{0: name})
	if err != nil {
		return err
	}
	if err := checkSimulationRooms(original, joined); err != nil {
		return err
	}
	if sim.senderIDs[0] == previousID {
		return fmt.Errorf("churn reconnect reused the old connection identity")
	}
	if _, err := sim.deliver("churn-reconnected", 1, sim.rooms, nil); err != nil {
		return err
	}
	if err := traffic.progress("traffic continues after reconnect"); err != nil {
		return err
	}
	if err := traffic.finish(); err != nil {
		return err
	}
	return sim.quiet()
}

func (sim *userSimulation) sizedChat(index int, prefix string, size int) (simulationMessage, error) {
	msg := sim.chat(sim.rooms[index], prefix)
	encoded, err := json.Marshal(msg)
	if err != nil {
		return msg, err
	}
	// WriteJSON uses Encoder.Encode, including a final newline. The read limit
	// applies to the entire UTF-8 JSON frame, not just the body or rune count.
	padding := size - len(encoded) - 1
	if padding < 0 {
		return msg, fmt.Errorf("requested frame size %d is smaller than its JSON envelope", size)
	}
	msg.Body += strings.Repeat("x", padding)
	return msg, nil
}

func (sim *userSimulation) payloadBoundaries() error {
	bodies := make([][]string, len(sim.peers))
	for index := range sim.peers {
		prefix := fmt.Sprintf("%s:payload:%d", sim.id, index)
		boundary, err := sim.sizedChat(index, prefix+":at-limit:", maxMessageSize)
		if err != nil {
			return err
		}
		bodies[index] = []string{
			prefix + ":こんにちは 👋 café — مرحبا",
			prefix + ":line one\nline two\r\nline three\t\"quoted\" \\slash",
			boundary.Body,
		}
	}
	if _, err := sim.deliverBodies("payload boundaries", sim.rooms, bodies, nil); err != nil {
		return err
	}
	oversized, err := sim.sizedChat(0, sim.id+":over-limit:", maxMessageSize+1)
	if err != nil {
		return err
	}
	notices := sim.newNotices()
	notices.expectLeave(0)
	closed := false
	err = sim.runPhase("oversized frame disconnect", len(notices.expected)+1, func(ctx context.Context, client int) error {
		if client != 0 {
			return nil
		}
		return sim.write(ctx, client, oversized)
	}, func(event simulationEvent) (bool, error) {
		return true, notices.accept(event)
	}, func(event simulationEvent) (bool, error) {
		if event.client != 0 || closed || !websocket.IsCloseError(event.err, websocket.CloseMessageTooBig) {
			return false, fmt.Errorf("unexpected disconnect for user %d: %w", event.client+1, event.err)
		}
		closed = true
		return true, nil
	})
	if err != nil {
		return err
	}
	if err := notices.complete(); err != nil {
		return err
	}
	sim.peers[0].close()
	sim.rooms[0] = nil
	if _, err := sim.deliver("after-oversize", 1, sim.rooms, nil); err != nil {
		return err
	}
	return sim.quiet()
}
