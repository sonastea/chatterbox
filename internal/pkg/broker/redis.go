package broker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

type redisBroker struct {
	lifecycle
	client *redis.Client
}

var _ Broker = (*redisBroker)(nil)

var redisLoggingOnce sync.Once

type redisLogger struct{}

func (redisLogger) Printf(ctx context.Context, format string, args ...interface{}) {
	slog.WarnContext(ctx, fmt.Sprintf(format, args...), "component", "redis")
}

func openRedis(ctx context.Context, address string) (Broker, error) {
	// go-redis otherwise uses its own plain-text stderr logger. Configure it once
	// before creating clients, including when hubs open brokers concurrently.
	redisLoggingOnce.Do(func() { redis.SetLogger(redisLogger{}) })
	// Valkey speaks the Redis protocol, including TLS and ACL authentication.
	address = strings.Replace(address, "valkey://", "redis://", 1)
	address = strings.Replace(address, "valkeys://", "rediss://", 1)
	options, err := redis.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("parse Redis/Valkey URL: %w", err)
	}
	options.ContextTimeoutEnabled = true
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("connect to Redis/Valkey: %w", err)
	}
	return &redisBroker{client: client}, nil
}

func (b *redisBroker) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := validateTopic(topic, false); err != nil {
		return err
	}
	if err := b.check(ctx); err != nil {
		return err
	}
	return b.client.Publish(ctx, topic, payload).Err()
}

func (b *redisBroker) Subscribe(ctx context.Context, pattern string) (Subscription, error) {
	if err := validateTopic(pattern, true); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	pubsub := b.client.PSubscribe(ctx, pattern)
	// Wait for the subscription acknowledgement before allowing publishers to send.
	if _, err := pubsub.Receive(ctx); err != nil {
		pubsub.Close()
		return nil, fmt.Errorf("subscribe to Redis/Valkey: %w", err)
	}
	input := pubsub.Channel(redis.WithChannelSize(bufferSize))
	return b.subscribe(ctx, func(ctx context.Context, messages chan<- Message) {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-input:
				if !ok {
					return
				}
				// Redis glob '*' also matches dots; enforce the common one-segment rule.
				if matches(pattern, msg.Channel) && !emit(ctx, messages, Message{Topic: msg.Channel, Payload: []byte(msg.Payload)}) {
					return
				}
			}
		}
	}, pubsub.Close), nil
}

func (b *redisBroker) Close() error { return b.close(b.client.Close) }
