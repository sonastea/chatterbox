package models

type Room interface {
	GetId() int
	GetXid() string
	GetPrivate() bool
	GetName() string
	GetDescription() string
	GetOwnerId() string
}

type RoomStore interface {
	AddRoom(room Room, owner_id string)
	FindRoomByName(name string) Room
	FindRoomByXid(xid string) Room
}
