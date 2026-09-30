package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

type natsBroker struct {
	lifecycle
	conn *nats.Conn
	done chan struct{}
}

var _ Broker = (*natsBroker)(nil)

func openNATS(ctx context.Context, address string) (Broker, error) {
	done := make(chan struct{})
	conn, err := nats.Connect(address, nats.Timeout(connectTimeout(ctx)), nats.MaxReconnects(-1),
		nats.ClosedHandler(func(*nats.Conn) { close(done) }), nats.ErrorHandler(logNATSError))
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}
	if err := ctx.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	return &natsBroker{conn: conn, done: done}, nil
}

func logNATSError(_ *nats.Conn, subscription *nats.Subscription, err error) {
	logger := slog.Default().With("component", "nats")
	if subscription != nil {
		logger = logger.With("topic", subscription.Subject)
	}
	logger.Error("nats asynchronous error", "error", err)
}

func (b *natsBroker) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := validateTopic(topic, false); err != nil {
		return err
	}
	if err := b.check(ctx); err != nil {
		return err
	}
	if err := b.conn.Publish(topic, payload); err != nil {
		return err
	}
	return b.flush(ctx)
}

func (b *natsBroker) Subscribe(ctx context.Context, pattern string) (Subscription, error) {
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
	input := make(chan *nats.Msg, bufferSize)
	sub, err := b.conn.ChanSubscribe(pattern, input)
	if err != nil {
		return nil, fmt.Errorf("subscribe to NATS: %w", err)
	}
	if err := b.flush(ctx); err != nil {
		sub.Unsubscribe()
		return nil, fmt.Errorf("flush NATS subscription: %w", err)
	}
	return b.subscribe(ctx, func(ctx context.Context, messages chan<- Message) {
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.done:
				return
			case msg, ok := <-input:
				if !ok || !emit(ctx, messages, Message{Topic: msg.Subject, Payload: msg.Data}) {
					return
				}
			}
		}
	}, func() error {
		if err := sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) && !errors.Is(err, nats.ErrBadSubscription) {
			return err
		}
		return nil
	}), nil
}

func (b *natsBroker) Close() error {
	return b.close(func() error {
		b.conn.Close()
		return nil
	})
}

func (b *natsBroker) flush(ctx context.Context) error {
	// NATS requires a deadline even when the caller uses context.Background().
	ctx, cancel := context.WithTimeout(ctx, connectTimeout(ctx))
	defer cancel()
	return b.conn.FlushWithContext(ctx)
}
