package broker

import "context"

type memorySubscriber struct {
	pattern string
	queue   chan Message
	sub     *subscription
}

type Memory struct {
	lifecycle
	listeners map[*memorySubscriber]struct{}
}

var _ Broker = (*Memory)(nil)

// NewMemory creates a process-local broker. Share an instance to connect multiple
// hubs in one process; use an external adapter to connect separate processes.
func NewMemory() *Memory {
	return &Memory{listeners: make(map[*memorySubscriber]struct{})}
}

func (b *Memory) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := validateTopic(topic, false); err != nil {
		return err
	}
	if err := b.check(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	listeners := make([]*memorySubscriber, 0, len(b.listeners))
	for listener := range b.listeners {
		if matches(listener.pattern, topic) {
			listeners = append(listeners, listener)
		}
	}
	b.mu.Unlock()
	for _, listener := range listeners {
		// Each subscriber owns its payload. Neither callers nor other subscribers
		// can mutate data already published to this subscription.
		msg := Message{Topic: topic, Payload: append([]byte(nil), payload...)}
		select {
		case listener.queue <- msg:
		case <-listener.sub.ctx.Done():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (b *Memory) Subscribe(ctx context.Context, pattern string) (Subscription, error) {
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
	listener := &memorySubscriber{pattern: pattern, queue: make(chan Message, bufferSize)}
	listener.sub = b.subscribe(ctx, func(ctx context.Context, messages chan<- Message) {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-listener.queue:
				if !emit(ctx, messages, msg) {
					return
				}
			}
		}
	}, func() error {
		b.mu.Lock()
		delete(b.listeners, listener)
		b.mu.Unlock()
		return nil
	})
	b.listeners[listener] = struct{}{}
	return listener.sub, nil
}

func (b *Memory) Close() error { return b.close(nil) }
