// Package broker provides transient pub/sub independent of the transport.
// Subscriptions are fan-out subscriptions, not competing consumers. Delivery is
// best-effort (no durable history or replay). '*' matches exactly one topic segment.
package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Topic   string
	Payload []byte
}

type Broker interface {
	Publish(ctx context.Context, topic string, payload []byte) error
	Subscribe(ctx context.Context, pattern string) (Subscription, error)
	Close() error
}

// Messages closes on cancellation, subscription closure, or transport failure.
// Close is idempotent and waits for subscription resources to be released.
type Subscription interface {
	Messages() <-chan Message
	Close() error
}

type Config struct {
	Driver string
	URL    string
}

var ErrClosed = errors.New("broker closed")

const bufferSize = 256

func Open(ctx context.Context, cfg Config) (Broker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "memory":
		return NewMemory(), nil
	case "redis", "valkey":
		if cfg.URL == "" {
			cfg.URL = "redis://localhost:6379/0"
		}
		return openRedis(ctx, cfg.URL)
	case "nats":
		if cfg.URL == "" {
			cfg.URL = "nats://localhost:4222"
		}
		return openNATS(ctx, cfg.URL)
	case "rabbitmq":
		if cfg.URL == "" {
			cfg.URL = "amqp://guest:guest@localhost:5672/"
		}
		return openRabbitMQ(ctx, cfg.URL)
	default:
		return nil, fmt.Errorf("unsupported broker driver %q (use memory, redis, valkey, nats, or rabbitmq)", cfg.Driver)
	}
}

// Topic validation keeps routing and wildcard semantics consistent across all backends.
func validateTopic(topic string, pattern bool) error {
	if len(topic) > 255 {
		return fmt.Errorf("invalid topic: exceeds the 255-byte routing key limit")
	}
	for _, segment := range strings.Split(topic, ".") {
		if pattern && segment == "*" {
			continue
		}
		if segment == "" {
			return fmt.Errorf("invalid topic %q: empty segment", topic)
		}
		for _, char := range segment {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
				continue
			}
			return fmt.Errorf("invalid topic %q: unsupported character %q", topic, char)
		}
	}
	return nil
}

func matches(pattern, topic string) bool {
	p, t := strings.Split(pattern, "."), strings.Split(topic, ".")
	if len(p) != len(t) {
		return false
	}
	for i := range p {
		if p[i] != "*" && p[i] != t[i] {
			return false
		}
	}
	return true
}

func connectTimeout(ctx context.Context) time.Duration {
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			return remaining
		}
	}
	return timeout
}

type subscription struct {
	ctx      context.Context
	cancel   context.CancelFunc
	messages chan Message
	done     chan struct{}
	err      error
}

func (s *subscription) Messages() <-chan Message { return s.messages }

func (s *subscription) Close() error {
	s.cancel()
	<-s.done
	return s.err
}

func emit(ctx context.Context, messages chan<- Message, msg Message) bool {
	select {
	case messages <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// lifecycle centralizes synchronized shutdown for every adapter. Callers hold mu
// while establishing subscriptions so Close cannot race with their registration.
type lifecycle struct {
	mu        sync.Mutex
	closed    bool
	subs      map[*subscription]struct{}
	closeOnce sync.Once
	closeErr  error
}

func (l *lifecycle) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	return nil
}

func (l *lifecycle) subscribe(ctx context.Context, read func(context.Context, chan<- Message), cleanup func() error) *subscription {
	ctx, cancel := context.WithCancel(ctx)
	s := &subscription{ctx: ctx, cancel: cancel, messages: make(chan Message), done: make(chan struct{})}
	if l.subs == nil {
		l.subs = make(map[*subscription]struct{})
	}
	l.subs[s] = struct{}{}
	go func() {
		read(ctx, s.messages)
		cancel()
		if cleanup != nil {
			s.err = cleanup()
		}
		l.mu.Lock()
		delete(l.subs, s)
		l.mu.Unlock()
		close(s.messages)
		close(s.done)
	}()
	return s
}

func (l *lifecycle) close(closeConnection func() error) error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		subs := make([]*subscription, 0, len(l.subs))
		for sub := range l.subs {
			subs = append(subs, sub)
		}
		l.mu.Unlock()
		for _, sub := range subs {
			l.closeErr = errors.Join(l.closeErr, sub.Close())
		}
		if closeConnection != nil {
			l.closeErr = errors.Join(l.closeErr, closeConnection())
		}
	})
	return l.closeErr
}
