package box

import (
	"context"
	"log"
	"strings"

	"github.com/go-redis/redis/v8"
)

type Realm string

type PubSub struct {
	conn          *redis.Client
	subs          []Realm
	subscriptions map[Realm]*redis.PubSub
}

var ctx = context.Background()

func newPubSub(opt *redis.Options) (*PubSub, error) {
	conn := redis.NewClient(opt)

	subs := []Realm{
		"room.*",
	}

	pubSub := &PubSub{
		conn:          conn,
		subs:          subs,
		subscriptions: make(map[Realm]*redis.PubSub),
	}

	for _, sub := range subs {
		ch := conn.PSubscribe(ctx, string(sub))
		pubSub.subscriptions[sub] = ch
	}

	return pubSub, nil
}

func (hub *Hub) listenPubSub() {
	for {
		select {
		case msg := <-hub.pubsub.subscriptions["room.*"].Channel():
			roomXid := strings.SplitN(msg.Channel, ".", 2)
			room := strings.TrimSpace(roomXid[1])
			if len(room) < 20 {
				log.Printf("Missing room XID %v \n", msg.String())
				break
			}
			hub.sendToRoom(roomXid[1], msg.Payload)
		}
	}
}
