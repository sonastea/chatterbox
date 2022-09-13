package database

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
)

func InitDB() error {
	log.Printf("Connect to database\n")
	db, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}

	db.Exec(context.Background(),
		`CREATE DATABASE Chatterbox
            WITH
            OWNER = postgres
            ENCODING = 'UTF8'
            CONNECTION LIMIT = -1
            IS_TEMPLATE = False;`)

	log.Printf("Read sql file\n")
	path := filepath.Join("./sql/script.sql")

	s, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	sql := string(s)

	log.Printf("Run sql script\n")
	db.Exec(context.Background(), sql)

	log.Printf("Close sql connection\n")
	return nil
}

func NewConnPool() *pgxpool.Pool {
	pool, err := pgxpool.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		panic("Unable to connect to database")
	}

	return pool
}
