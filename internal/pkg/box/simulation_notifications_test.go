package box

import (
	"context"
	"fmt"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

type simulationNoticeKey struct {
	client   int
	actor    int
	action   string
	roomName string
}

type simulationNotices struct {
	sim       *userSimulation
	expected  map[simulationNoticeKey]bool
	newcomers map[int]string
	rooms     map[string]*store.Room
}

func checkSimulationServerMessage(msg simulationMessage) error {
	if msg.Type != message.Server.String() || msg.Room == nil || msg.Room.Xid == "" || msg.Room.Name == "" {
		return fmt.Errorf("invalid server notification: %+v", msg)
	}
	if msg.Sender == nil || msg.Sender.Xid != xid.NilID().String() || msg.Sender.Name != "SERVER" {
		return fmt.Errorf("invalid server notification sender: %+v", msg.Sender)
	}
	switch msg.Action {
	case message.NotifyJoinRoomMessage.String(), message.JoinRoomMessage.String(), message.LeaveRoomMessage.String():
		return nil
	default:
		return fmt.Errorf("unexpected server notification action %q", msg.Action)
	}
}

func (sim *userSimulation) newNotices() *simulationNotices {
	notices := &simulationNotices{
		sim: sim, expected: make(map[simulationNoticeKey]bool),
		newcomers: make(map[int]string), rooms: make(map[string]*store.Room),
	}
	for _, room := range sim.rooms {
		if room != nil {
			notices.rooms[room.Name] = room
		}
	}
	return notices
}

func (notices *simulationNotices) expectLeave(actor int) {
	room := notices.sim.rooms[actor]
	if room == nil {
		return
	}
	for client, current := range notices.sim.rooms {
		if client != actor && current != nil && current.Xid == room.Xid {
			notices.expected[simulationNoticeKey{client, actor, message.LeaveRoomMessage.String(), room.Name}] = false
		}
	}
}

func (sim *userSimulation) joinNotices(names map[int]string) (*simulationNotices, error) {
	notices := sim.newNotices()
	for actor, name := range names {
		old := sim.rooms[actor]
		if old != nil && old.Name == name {
			continue // An idempotent join acknowledges, but does not announce, membership.
		}
		if old != nil {
			// Switching several sockets simultaneously makes announcement recipients
			// dependent on hub scheduling. Exercise switches individually instead.
			if len(names) != 1 {
				return nil, fmt.Errorf("simulation room switches must be issued individually")
			}
			notices.expectLeave(actor)
		}
		notices.newcomers[actor] = name
	}
	for actor, name := range notices.newcomers {
		for client, current := range sim.rooms {
			if current != nil && current.Name == name && notices.newcomers[client] == "" {
				notices.expected[simulationNoticeKey{client, actor, message.JoinRoomMessage.String(), name}] = false
			}
		}
		for client, target := range notices.newcomers {
			if actor < client && name == target {
				// For concurrent joins, exactly one member of each pair observes
				// the other's arrival. Either orientation is valid.
				notices.expected[simulationNoticeKey{client, actor, message.JoinRoomMessage.String(), name}] = false
			}
		}
	}
	return notices, nil
}

func (notices *simulationNotices) accept(event simulationEvent) error {
	msg := event.message
	if err := checkSimulationServerMessage(msg); err != nil {
		return err
	}
	room := notices.rooms[msg.Room.Name]
	if room == nil || room.Xid != msg.Room.Xid {
		return fmt.Errorf("notification references the wrong room: %+v", msg.Room)
	}
	var suffix string
	switch msg.Action {
	case message.JoinRoomMessage.String():
		suffix = " has joined. Say hi."
	case message.LeaveRoomMessage.String():
		suffix = " left the room."
	default:
		return fmt.Errorf("unexpected membership notification action %q", msg.Action)
	}
	identity := strings.TrimSuffix(msg.Body, suffix)
	actor := -1
	for index, known := range notices.sim.senderIDs {
		if known != "" && known == identity {
			actor = index
			break
		}
	}
	if actor == -1 || identity == msg.Body {
		return fmt.Errorf("invalid notification body/actor %q", msg.Body)
	}
	key := simulationNoticeKey{event.client, actor, msg.Action, msg.Room.Name}
	if key.action == message.JoinRoomMessage.String() && notices.newcomers[key.client] == key.roomName && notices.newcomers[key.actor] == key.roomName && key.client < key.actor {
		key.client, key.actor = key.actor, key.client
	}
	seen, expected := notices.expected[key]
	if !expected {
		return fmt.Errorf("notification sent to the wrong recipient or for an unexpected actor: %+v", key)
	}
	if seen {
		return fmt.Errorf("duplicate membership notification: %+v", key)
	}
	notices.expected[key] = true
	return nil
}

func (notices *simulationNotices) complete() error {
	for key, seen := range notices.expected {
		if !seen {
			return fmt.Errorf("missing membership notification: %+v", key)
		}
	}
	return nil
}

func (sim *userSimulation) leave(index int, repeat bool) error {
	room := sim.rooms[index]
	notices := sim.newNotices()
	notices.expectLeave(index)
	barrier := fmt.Sprintf("%s:left:%d", sim.id, index)
	pongSeen := false
	err := sim.phase("leave rooms", len(notices.expected)+1, func(ctx context.Context, client int) error {
		if client != index {
			return nil
		}
		command := simulationMessage{Type: message.Command.String(), Action: message.LeaveRoom.String(), Room: room}
		if err := sim.write(ctx, client, command); err != nil {
			return err
		}
		if repeat {
			if err := sim.write(ctx, client, command); err != nil {
				return err
			}
		}
		if err := sim.write(ctx, client, sim.chat(room, sim.id+":after-leave")); err != nil {
			return err
		}
		// Commands are submitted synchronously to the hub. Receiving this pong
		// ensures both leaves and the unauthorized publish have been submitted.
		deadline, _ := ctx.Deadline()
		return sim.peers[client].conn.WriteControl(websocket.PingMessage, []byte(barrier), deadline)
	}, func(event simulationEvent) (bool, error) {
		if event.client == index && event.pong == barrier && !pongSeen {
			pongSeen = true
			return true, nil
		}
		return true, notices.accept(event)
	})
	if err != nil {
		return err
	}
	if err := notices.complete(); err != nil {
		return err
	}
	sim.rooms[index] = nil
	return nil
}

func (sim *userSimulation) disconnect(index int) error {
	notices := sim.newNotices()
	notices.expectLeave(index)
	err := sim.phase("disconnect", len(notices.expected), func(_ context.Context, client int) error {
		if client == index {
			sim.peers[client].close()
		}
		return nil
	}, func(event simulationEvent) (bool, error) {
		return true, notices.accept(event)
	})
	if err != nil {
		return err
	}
	if err := notices.complete(); err != nil {
		return err
	}
	sim.rooms[index] = nil
	return nil
}
