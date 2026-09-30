package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sonastea/chatterbox/internal/pkg/database"
)

// RoomRepository is the persistence boundary used by the chat hub.
// Implementations need not use SQL. Missing records return ErrNotFound.
type RoomRepository interface {
	AddRoom(ctx context.Context, room Room) (*Room, error)
	FindRoomByName(ctx context.Context, name string) (*Room, error)
	FindRoomByXid(ctx context.Context, xid string) (*Room, error)
}

type RoomStore struct {
	DB *database.DB
}

var ErrNotFound = errors.New("record not found")

var _ RoomRepository = (*RoomStore)(nil)

type Room struct {
	ID          int    `json:"id,omitempty"`
	Xid         string `json:"xid"`
	Private     bool   `json:"private"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Owner_ID    string `json:"owner_id"`
}

func (room *Room) GetId() int {
	return room.ID
}

func (room *Room) GetXid() string {
	return room.Xid
}

func (room *Room) GetPrivate() bool {
	return room.Private
}

func (room *Room) GetName() string {
	return room.Name
}

func (room *Room) GetDescription() string {
	return room.Description
}

func (room *Room) GetOwnerId() string {
	return room.Owner_ID
}

func (rs *RoomStore) AddRoom(ctx context.Context, room Room) (*Room, error) {
	query := fmt.Sprintf(`INSERT INTO %s(xid, private, name, description, owner_id)
        VALUES($1, $2, $3, $4, $5)
        RETURNING id, xid, private, name, COALESCE(description, ''), owner_id`, rs.DB.Dialect.Table("Room"))
	return scanRoom(rs.DB.QueryRowContext(ctx, query, room.Xid, room.Private, room.Name, room.Description, room.Owner_ID))
}

func (rs *RoomStore) FindRoomByName(ctx context.Context, name string) (*Room, error) {
	query := fmt.Sprintf(`SELECT id, xid, private, name, COALESCE(description, ''), owner_id
        FROM %s WHERE name = $1 LIMIT 1`, rs.DB.Dialect.Table("Room"))
	return scanRoom(rs.DB.QueryRowContext(ctx, query, name))
}

func (rs *RoomStore) FindRoomByXid(ctx context.Context, xid string) (*Room, error) {
	query := fmt.Sprintf(`SELECT id, xid, private, name, COALESCE(description, ''), owner_id
        FROM %s WHERE xid = $1 LIMIT 1`, rs.DB.Dialect.Table("Room"))
	return scanRoom(rs.DB.QueryRowContext(ctx, query, xid))
}

func scanRoom(row *sql.Row) (*Room, error) {
	var room Room
	err := row.Scan(&room.ID, &room.Xid, &room.Private, &room.Name, &room.Description, &room.Owner_ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read room: %w", err)
	}
	return &room, nil
}
