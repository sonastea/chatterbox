package store

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/sonastea/chatterbox/internal/pkg/models"
)

type UserStore struct {
	DB *pgxpool.Pool
}

type User struct {
	Id       int    `json:"id"`
	Xid      string `json:"xid"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (user *User) GetId() int {
	return user.Id
}

func (user *User) GetXid() string {
	return user.Xid
}

func (user *User) GetName() string {
	return user.Name
}

func (user *User) GetEmail() string {
	return user.Email
}

func (user *User) GetPassword() string {
	return user.Password
}

func (us *UserStore) AddUser(client models.User) models.User {
	stmt, err := us.DB.Query(
		context.Background(),
		`INSERT INTO chatterbox."User"(xid, name, email, password) VALUES($1, $2, $3, $4)`,
		client.GetXid(), client.GetName(), client.GetName()+"@example.com", client.GetPassword(),
	)
	if err != nil {
		log.Println(err)
	}
	defer stmt.Close()

	var user User
	if stmt.Next() {
		if err := stmt.Scan(&user.Xid, &user.Name, &user.Email); err != nil {
			log.Println(err)
		}
	}

	return &user
}

func (us *UserStore) RemoveUser(client models.User) {
	stmt, err := us.DB.Query(
		context.Background(),
		`DELETE from chatterbox."User" WHERE name = $1`,
		client.GetName(),
	)
	if err != nil {
		log.Println(err)
	}

	defer stmt.Close()
}

func (us *UserStore) FindUserByXid(xid string) models.User {
	row, err := us.DB.Query(
		context.Background(),
		`SELECT xid, name, email from chatterbox."User" WHERE xid = $1 LIMIT 1;`,
		xid,
	)
	if err != nil {
		log.Println(err)
	}
	defer row.Close()

	var user User
	if row.Next() {
		if err := row.Scan(&user.Xid, &user.Name, &user.Email); err != nil {
			log.Println(err)
		}
	}
	fmt.Printf("%+v", user)

	return &user
}

func (us *UserStore) GetAllUsers() []models.User {
	rows, err := us.DB.Query(context.Background(), `SELECT xid, name, email FROM chatterbox."User"`)
	if err != nil {
		log.Println(err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var user User
		rows.Scan(&user.Xid, &user.Name, &user.Email)
		users = append(users, &user)
	}

	return users
}
