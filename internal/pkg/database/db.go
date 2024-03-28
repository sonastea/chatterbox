package database

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
)

func InitDB(ctx context.Context) error {
	log.Printf("Connect to database\n")
	db, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}

	db.Exec(ctx,
		`CREATE DATABASE Chatterbox
            WITH
            OWNER = postgres
            ENCODING = 'UTF8'
            CONNECTION LIMIT = -1
            IS_TEMPLATE = False;`)

	log.Printf("Read sql file\n")
	path := filepath.Join("./sql/script.sql")

	s, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sql := string(s)

	log.Printf("Run sql script\n")
	db.Exec(ctx, sql)

	log.Printf("Close sql connection\n")
	return nil
}

func NewConnPool(ctx context.Context) *pgxpool.Pool {
	pool, err := pgxpool.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic("Unable to connect to database")
	}

	return pool
}
