package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

// Gate subscription reads to reproduce a full memory queue without relying on
// random hub scheduling or enough WebSocket load to disconnect healthy clients.
// The short publish deadline keeps this failure/recovery scenario fast.
type simulationBackpressureBroker struct {
	broker.Broker
	readable atomic.Bool
	started  chan struct{}
	result   chan error
}

type simulationGatedSubscription struct {
	broker.Subscription
	readable *atomic.Bool
}

func (sub *simulationGatedSubscription) Messages() <-chan broker.Message {
	if !sub.readable.Load() {
		return nil
	}
	return sub.Subscription.Messages()
}

func (bus *simulationBackpressureBroker) Subscribe(ctx context.Context, pattern string) (broker.Subscription, error) {
	sub, err := bus.Broker.Subscribe(ctx, pattern)
	if err != nil {
		return nil, err
	}
	return &simulationGatedSubscription{Subscription: sub, readable: &bus.readable}, nil
}

func (bus *simulationBackpressureBroker) Publish(ctx context.Context, topic string, payload []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	select {
	case bus.started <- struct{}{}:
	default:
	}
	err := bus.Broker.Publish(ctx, topic, payload)
	select {
	case bus.result <- err:
	default:
	}
	return err
}

func testSimulationBackpressureRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := database.Open(ctx, database.Config{URL: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bus := &simulationBackpressureBroker{
		Broker: broker.NewMemory(), started: make(chan struct{}, 1), result: make(chan error, 1),
	}
	bus.readable.Store(true)
	t.Cleanup(func() { bus.Close() })
	address, _ := testServer(t, ctx, db, bus)
	sim := &userSimulation{
		ctx: ctx, id: "backpressure-" + xid.New().String(),
		cfg:   simulationConfig{URL: address, Timeout: 5 * time.Second, Interval: 20 * time.Millisecond},
		peers: make([]*simulationPeer, 2), senderIDs: make([]string, 2), events: make(chan simulationEvent, 32),
	}
	t.Cleanup(sim.close)
	for client := range sim.peers {
		if err := sim.connect(client); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sim.join(map[int]string{0: sim.id, 1: sim.id}); err != nil {
		t.Fatal(err)
	}
	bus.readable.Store(false)
	// Seed an inactive room so draining the backlog cannot fill client send
	// queues. Detect capacity via the deadline rather than copying bufferSize.
	fill, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	topic := "room." + xid.New().String()
	for attempt := 0; attempt < 4096; attempt++ {
		err = bus.Broker.Publish(fill, topic, []byte("inactive-room backlog"))
		if err != nil {
			break
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("could not saturate the memory subscription: %v", err)
	}
	room := sim.rooms[0]
	blockedBody, fenceBody := sim.id+":saturated", sim.id+":drained"
	seenBlocked, seenFence := make(map[int]bool), make(map[int]bool)
	var publishErr error
	err = sim.phase("saturated broker recovery", len(sim.peers), func(ctx context.Context, client int) error {
		if client != 0 {
			return nil
		}
		if err := sim.write(ctx, client, sim.chat(room, blockedBody)); err != nil {
			return err
		}
		select {
		case <-bus.started:
		case <-ctx.Done():
			return ctx.Err()
		}
		bus.readable.Store(true)
		select {
		case publishErr = <-bus.result:
		case <-ctx.Done():
			return ctx.Err()
		}
		if publishErr != nil && !errors.Is(publishErr, context.DeadlineExceeded) {
			return publishErr
		}
		// Publish from outside the hub: this can wait for capacity while the
		// hub drains its backlog. Its delivery fences all earlier queued events.
		fence, err := json.Marshal(simulationMessage{
			Type: message.Normal.String(), Action: message.SendMessage.String(), Room: room,
			Body: fenceBody, Sender: &store.User{Xid: sim.senderIDs[0]},
		})
		if err != nil {
			return err
		}
		return bus.Broker.Publish(ctx, "room."+room.Xid, fence)
	}, func(event simulationEvent) (bool, error) {
		msg := event.message
		if msg.Type != message.Normal.String() || msg.Action != message.SendMessage.String() || msg.Room == nil || msg.Room.Xid != room.Xid || msg.Sender == nil || msg.Sender.Xid != sim.senderIDs[0] {
			return false, fmt.Errorf("invalid recovery delivery: %+v", msg)
		}
		switch msg.Body {
		case blockedBody:
			if seenBlocked[event.client] || seenFence[event.client] {
				return false, fmt.Errorf("duplicate or out-of-order saturated publication")
			}
			seenBlocked[event.client] = true
			return false, nil
		case fenceBody:
			if seenFence[event.client] {
				return false, fmt.Errorf("duplicate recovery fence")
			}
			seenFence[event.client] = true
			return true, nil
		default:
			return false, fmt.Errorf("unexpected recovery message %q", msg.Body)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// Best-effort fan-out may lose the saturated publish on its deadline. If a
	// future hub implementation drains concurrently, successful delivery is
	// also valid, but it must reach both clients exactly once.
	wantBlocked := 0
	if publishErr == nil {
		wantBlocked = len(sim.peers)
	}
	if len(seenBlocked) != wantBlocked {
		t.Fatalf("saturated publication delivered to %d clients, want %d (publish error: %v)", len(seenBlocked), wantBlocked, publishErr)
	}
	if _, err := sim.deliver("backpressure-recovered", 2, sim.rooms, nil); err != nil {
		t.Fatal(err)
	}
	if err := sim.quiet(); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS: a saturated broker queue does not prevent subsequent ordered chat delivery.")
}
