package models

type User interface {
	GetId() int
	GetXid() string
	GetName() string
	GetEmail() string
	GetPassword() string
}

type UserStore interface {
	AddUser(user User) User
	RemoveUser(user User)
	FindUserByXid(xid string) User
	GetAllUsers() []User
}
