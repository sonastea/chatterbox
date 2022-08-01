package store

import (
	"context"
	"log"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/sonastea/chatterbox/internal/pkg/models"
)

type RoomStore struct {
	DB *pgxpool.Pool
}

type Room struct {
	Id          int    `json:"id,omitempty"`
	Xid         string `json:"xid"`
	Private     bool   `json:"private"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Owner_Id    string `json:"owner_id"`
}

func (room *Room) GetId() int {
	return room.Id
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
	return room.Owner_Id
}

func (rs *RoomStore) AddRoom(room models.Room, owner_id string) {
	stmt, err := rs.DB.Query(
		context.Background(),
		`INSERT INTO chatterbox."Room"(xid, name, description, owner_id) VALUES($1, $2, $3, $4)`,
		room.GetXid(), room.GetName(), room.GetDescription(), owner_id,
	)
	if err != nil {
		log.Println(err)
	}
	defer stmt.Close()
}

func (rs *RoomStore) FindRoomByName(name string) models.Room {
	stmt, err := rs.DB.Query(
		context.Background(),
		`SELECT xid, private, name, description, owner_id from chatterbox."Room" WHERE name = $1 LIMIT 1;`,
		name,
	)
	if err != nil {
		log.Println(err)
	}
	defer stmt.Close()

	var room Room
	row := stmt.Next()
	if row == false {
		return nil
	}

	if err := stmt.Scan(&room.Xid, &room.Private, &room.Name, &room.Description, &room.Owner_Id); err != nil {
		log.Println(err)
	}

	return &room
}

func (rs *RoomStore) FindRoomByXid(xid string) models.Room {
	stmt, err := rs.DB.Query(context.Background(), `SELECT from chatterbox."Room" WHERE xid = $1`, xid)
	if err != nil {
		log.Println(err)
	}
	defer stmt.Close()

	var room Room
	for stmt.Next() {
		if err := stmt.Scan(&room.Xid, &room.Private, &room.Name, &room.Description); err != nil {
			log.Println(err)
		}
	}

	return &room
}
