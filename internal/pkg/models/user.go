package models

type User interface {
	GetId() int
	GetXid() string
	GetName() string
	GetEmail() string
	GetPassword() string
}

type UserStore interface {
	AddUser(user User) (User, error)
	RemoveUser(user User) error
	FindUserByXid(xid string) (User, error)
	GetAllUsers() ([]User, error)
}
