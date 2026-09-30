package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const rabbitExchange = "chatterbox"

type rabbitBroker struct {
	lifecycle
	conn      *amqp.Connection
	publisher *amqp.Channel
	publishMu sync.Mutex
}

var _ Broker = (*rabbitBroker)(nil)

func openRabbitMQ(ctx context.Context, address string) (Broker, error) {
	timeout := connectTimeout(ctx)
	conn, err := amqp.DialConfig(address, amqp.Config{
		Dial: func(network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, address)
			if err == nil {
				// Bound the AMQP/TLS handshake as well as the TCP connection.
				err = conn.SetDeadline(time.Now().Add(timeout))
				if err != nil {
					conn.Close()
				}
			}
			return conn, err
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect to RabbitMQ: %w", err)
	}
	channel, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := channel.ExchangeDeclare(rabbitExchange, "topic", true, false, false, false, nil); err != nil {
		channel.Close()
		conn.Close()
		return nil, fmt.Errorf("declare RabbitMQ exchange: %w", err)
	}
	if err := channel.Confirm(false); err != nil {
		channel.Close()
		conn.Close()
		return nil, fmt.Errorf("enable RabbitMQ confirms: %w", err)
	}
	if err := ctx.Err(); err != nil {
		channel.Close()
		conn.Close()
		return nil, err
	}
	return &rabbitBroker{conn: conn, publisher: channel}, nil
}

func (b *rabbitBroker) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := validateTopic(topic, false); err != nil {
		return err
	}
	if err := b.check(ctx); err != nil {
		return err
	}
	b.publishMu.Lock()
	defer b.publishMu.Unlock()
	confirmation, err := b.publisher.PublishWithDeferredConfirmWithContext(ctx, rabbitExchange, topic, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        payload,
	})
	if err != nil {
		return fmt.Errorf("publish to RabbitMQ: %w", err)
	}
	ack, err := confirmation.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ack {
		return fmt.Errorf("RabbitMQ rejected publication")
	}
	return nil
}

func (b *rabbitBroker) Subscribe(ctx context.Context, pattern string) (Subscription, error) {
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
	channel, err := b.conn.Channel()
	if err != nil {
		return nil, err
	}
	// Every subscription has its own temporary queue: all server instances receive
	// each event, rather than competing for messages on a shared work queue.
	queue, err := channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		channel.Close()
		return nil, err
	}
	if err := channel.QueueBind(queue.Name, pattern, rabbitExchange, false, nil); err != nil {
		channel.Close()
		return nil, err
	}
	if err := channel.Qos(bufferSize, 0, false); err != nil {
		channel.Close()
		return nil, err
	}
	input, err := channel.Consume(queue.Name, "", false, true, false, false, nil)
	if err != nil {
		channel.Close()
		return nil, err
	}
	return b.subscribe(ctx, func(ctx context.Context, messages chan<- Message) {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-input:
				if !ok || !emit(ctx, messages, Message{Topic: msg.RoutingKey, Payload: msg.Body}) {
					return
				}
				if err := msg.Ack(false); err != nil {
					return
				}
			}
		}
	}, func() error { return ignoreAMQPClosed(channel.Close()) }), nil
}

func (b *rabbitBroker) Close() error {
	return b.close(func() error {
		b.publishMu.Lock()
		defer b.publishMu.Unlock()
		return errors.Join(ignoreAMQPClosed(b.publisher.Close()), ignoreAMQPClosed(b.conn.Close()))
	})
}

func ignoreAMQPClosed(err error) error {
	if errors.Is(err, amqp.ErrClosed) {
		return nil
	}
	return err
}
