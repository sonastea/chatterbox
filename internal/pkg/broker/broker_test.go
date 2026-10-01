package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rs/xid"
)

func TestIntegrationBrokerContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		bus := NewMemory()
		testContract(t, bus, bus)
	})
	for _, driver := range []string{"redis", "valkey", "nats", "rabbitmq"} {
		t.Run(driver, func(t *testing.T) {
			variable := "TEST_" + map[string]string{"redis": "REDIS", "valkey": "VALKEY", "nats": "NATS", "rabbitmq": "RABBITMQ"}[driver] + "_URL"
			address := os.Getenv(variable)
			if address == "" {
				t.Skip("set " + variable + " or use go run ./tests/integration")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			publisher, err := Open(ctx, Config{Driver: driver, URL: address})
			if err != nil {
				t.Fatal(err)
			}
			defer publisher.Close()
			receiver, err := Open(ctx, Config{Driver: driver, URL: address})
			if err != nil {
				t.Fatal(err)
			}
			testContract(t, publisher, receiver)
		})
	}
}

func testContract(t *testing.T, publisher, receiver Broker) {
	t.Helper()
	defer publisher.Close()
	defer receiver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prefix := "test." + xid.New().String()
	topic := prefix + ".room"
	if err := publisher.Publish(ctx, topic, []byte("no-replay")); err != nil {
		t.Fatal(err)
	}
	first, err := receiver.Subscribe(ctx, prefix+".*")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := receiver.Subscribe(ctx, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// Redis glob patterns must not make '*' match multiple topic segments.
	if err := publisher.Publish(ctx, topic+".extra", []byte("wrong-topic")); err != nil {
		t.Fatal(err)
	}
	payload := []byte{'a', 0, 'b', '\n', 255}
	expected := append([]byte(nil), payload...)
	// Background contexts are supported, including by the NATS adapter.
	if err := publisher.Publish(context.Background(), topic, payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'x'
	for _, sub := range []Subscription{first, second} {
		msg := receive(t, sub)
		if msg.Topic != topic || !bytes.Equal(msg.Payload, expected) {
			t.Fatalf("received %+v, expected topic %s and payload %v", msg, topic, expected)
		}
		// Mutating one subscriber's payload cannot affect another's.
		msg.Payload[0] = 'y'
	}
	for i := 0; i < 10; i++ {
		data := []byte(fmt.Sprint(i))
		if err := publisher.Publish(ctx, topic, data); err != nil {
			t.Fatal(err)
		}
		for _, sub := range []Subscription{first, second} {
			if msg := receive(t, sub); !bytes.Equal(msg.Payload, data) {
				t.Fatalf("out-of-order message: %q, expected %q", msg.Payload, data)
			}
		}
	}
	cancelCtx, stop := context.WithCancel(ctx)
	third, err := receiver.Subscribe(cancelCtx, prefix+".*")
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ctx, topic, []byte("pending")); err != nil {
		t.Fatal(err)
	}
	stop()
	waitClosed(t, third)
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, first)
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, second)
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Subscribe(ctx, topic); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after close error = %v", err)
	}
	if err := receiver.Publish(ctx, topic, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("publish after close error = %v", err)
	}
}

func receive(t *testing.T, sub Subscription) Message {
	t.Helper()
	select {
	case msg, ok := <-sub.Messages():
		if !ok {
			t.Fatal("subscription unexpectedly closed")
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
		return Message{}
	}
}

func waitClosed(t *testing.T, sub Subscription) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-sub.Messages():
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("subscription did not close")
		}
	}
}

func TestMemoryBackpressure(t *testing.T) {
	bus := NewMemory()
	defer bus.Close()
	sub, err := bus.Subscribe(context.Background(), "room.*")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	for i := 0; i < bufferSize+2; i++ {
		err = bus.Publish(ctx, "room.one", []byte("unread"))
		if err != nil {
			break
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("full subscription should honor publish deadline, got %v", err)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), "room.one", nil); err != nil {
		t.Fatalf("closed slow subscriber should not block: %v", err)
	}
}

func TestMemoryConcurrentShutdown(t *testing.T) {
	bus := NewMemory()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 10; i++ {
		initial, err := bus.Subscribe(ctx, "room.*")
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(initial Subscription) {
			defer wg.Done()
			defer initial.Close()
			<-start
			for j := 0; j < 100; j++ {
				sub, err := bus.Subscribe(ctx, "room.*")
				if errors.Is(err, ErrClosed) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				if err := bus.Publish(ctx, "room.one", []byte("data")); err != nil && !errors.Is(err, ErrClosed) {
					t.Error(err)
				}
				sub.Close()
			}
		}(initial)
	}
	close(start)
	if err := bus.Publish(ctx, "room.one", []byte("shutdown-race")); err != nil {
		t.Fatal(err)
	}
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestTopicValidation(t *testing.T) {
	bus := NewMemory()
	defer bus.Close()
	for _, topic := range []string{"", ".room", "room.", "room..one", "room.*", "room.>", "room.#", "room.hello world", "room.[abc]"} {
		if err := bus.Publish(context.Background(), topic, nil); err == nil {
			t.Errorf("accepted invalid publish topic %q", topic)
		}
	}
	for _, pattern := range []string{"", "room.>", "room.#", "room.a*", "room..*"} {
		if sub, err := bus.Subscribe(context.Background(), pattern); err == nil {
			sub.Close()
			t.Errorf("accepted invalid pattern %q", pattern)
		}
	}
	for _, topic := range []string{"room.one", "room.a-b_c9", "one"} {
		if err := bus.Publish(context.Background(), topic, nil); err != nil {
			t.Error(err)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	if bus, err := Open(context.Background(), Config{Driver: "unknown"}); err == nil {
		bus.Close()
		t.Fatal("unsupported driver should fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if bus, err := Open(ctx, Config{}); !errors.Is(err, context.Canceled) {
		if bus != nil {
			bus.Close()
		}
		t.Fatalf("cancelled open = %v", err)
	}
}

func TestIntegrationTransportClosure(t *testing.T) {
	for _, driver := range []string{"nats", "rabbitmq"} {
		t.Run(driver, func(t *testing.T) {
			variable := "TEST_" + map[string]string{"nats": "NATS", "rabbitmq": "RABBITMQ"}[driver] + "_URL"
			address := os.Getenv(variable)
			if address == "" {
				t.Skip("set " + variable + " or use go run ./tests/integration")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			bus, err := Open(ctx, Config{Driver: driver, URL: address})
			if err != nil {
				t.Fatal(err)
			}
			defer bus.Close()
			sub, err := bus.Subscribe(ctx, "test."+xid.New().String()+".*")
			if err != nil {
				t.Fatal(err)
			}
			switch adapter := bus.(type) {
			case *natsBroker:
				adapter.conn.Close()
			case *rabbitBroker:
				if err := adapter.conn.Close(); err != nil {
					t.Fatal(err)
				}
			}
			waitClosed(t, sub)
			if err := bus.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
