package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sonastea/chatterbox/internal/pkg/database"
)

type UserRepository interface {
	AddUser(ctx context.Context, user User) (*User, error)
	RemoveUser(ctx context.Context, xid string) error
	FindUserByXid(ctx context.Context, xid string) (*User, error)
	GetAllUsers(ctx context.Context) ([]User, error)
}

type UserStore struct {
	DB *database.DB
}

var _ UserRepository = (*UserStore)(nil)

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

func (us *UserStore) AddUser(ctx context.Context, client User) (*User, error) {
	query := fmt.Sprintf(`INSERT INTO %s(xid, name, email, password) VALUES($1, $2, $3, $4)
        RETURNING id, xid, name, email`, us.DB.Dialect.Table("User"))
	var user User
	err := us.DB.QueryRowContext(ctx, query, client.Xid, client.Name, client.Email, client.Password).
		Scan(&user.Id, &user.Xid, &user.Name, &user.Email)
	if err != nil {
		return nil, fmt.Errorf("add user: %w", err)
	}
	return &user, nil
}

func (us *UserStore) RemoveUser(ctx context.Context, xid string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE xid = $1`, us.DB.Dialect.Table("User"))
	_, err := us.DB.ExecContext(ctx, query, xid)
	if err != nil {
		return fmt.Errorf("remove user: %w", err)
	}
	return nil
}

func (us *UserStore) FindUserByXid(ctx context.Context, xid string) (*User, error) {
	query := fmt.Sprintf(`SELECT id, xid, name, email FROM %s WHERE xid = $1 LIMIT 1`, us.DB.Dialect.Table("User"))
	var user User
	err := us.DB.QueryRowContext(ctx, query, xid).Scan(&user.Id, &user.Xid, &user.Name, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by xid: %w", err)
	}
	return &user, nil
}

func (us *UserStore) GetAllUsers(ctx context.Context) ([]User, error) {
	query := fmt.Sprintf(`SELECT id, xid, name, email FROM %s ORDER BY id`, us.DB.Dialect.Table("User"))
	rows, err := us.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get users: %w", err)
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.Id, &user.Xid, &user.Name, &user.Email); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read users: %w", err)
	}
	return users, nil
}
