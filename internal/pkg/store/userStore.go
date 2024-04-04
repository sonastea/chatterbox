package store

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
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

func (us *UserStore) AddUser(client models.User) (models.User, error) {
	query :=
		`INSERT INTO chatterbox."User"(xid, name, email, password) VALUES($1, $2, $3, $4)
            RETURNING xid, name, email`

	var user User
	err := us.DB.QueryRow(
		context.Background(),
		query,
		client.GetXid(), client.GetName(), client.GetName()+"@example.com", client.GetPassword()).
		Scan(&user.Xid, &user.Name, &user.Email)

	if err != nil {
		log.Printf("Error adding user: %v\n", err)
		return nil, err
	}

	return &user, err
}

func (us *UserStore) RemoveUser(client models.User) error {
	query := `DELETE from chatterbox."User" WHERE name = $1`

	_, err := us.DB.Exec(context.Background(), query, client.GetName())
	if err != nil {
		log.Printf("Error removing user: %v\n", err)
		return fmt.Errorf("Error removing user: %w\n", err)
	}

	return nil
}

func (us *UserStore) FindUserByXid(xid string) (models.User, error) {
	query := `SELECT xid, name, email from chatterbox."User" WHERE xid = $1 LIMIT 1`

	var user User
	err := us.DB.QueryRow(context.Background(), query, xid).Scan(&user.Xid, &user.Name, &user.Email)
	if err != nil {
		log.Printf("Error finding user by xid: %v\n", err)
		return nil, fmt.Errorf("Error finding user by xid: %w\n", err)
	}

	return &user, nil
}

func (us *UserStore) GetAllUsers() ([]models.User, error) {
	query := `SELECT xid, name, email FROM chatterbox."User"`

	rows, err := us.DB.Query(context.Background(), query)
	if err != nil {
		log.Printf("Error getting all users: %v\n", err)
		return nil, fmt.Errorf("Error getting all users: %w\n", err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Xid, &user.Name, &user.Email); err != nil {
			log.Printf("Error scanning user row: %v\n", err)
			return nil, fmt.Errorf("Error scanning user row: %w\n", err)

		}
		users = append(users, &user)
	}

	return users, nil
}
