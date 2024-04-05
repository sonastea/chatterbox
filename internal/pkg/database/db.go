package database

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func InitDB(ctx context.Context) error {
	log.Printf("Connect to database for migrations\n")
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Printf("[Connect] %v\n", err.Error())
		return err
	}
	defer db.Close()

	_, err = db.Exec(ctx,
		`CREATE DATABASE Chatterbox
            WITH
            OWNER = postgres
            ENCODING = 'UTF8'
            CONNECTION LIMIT = -1
            IS_TEMPLATE = False;`)
	if err != nil {
		if strings.Contains(err.Error(), "42P04") {
			log.Println("Database Chatterbox already exists")
		} else {
			return err
		}
	}

	log.Printf("Read sql file\n")
	path := filepath.Join("./sql/script.sql")

	s, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sql := string(s)

	log.Printf("Run sql script\n")
    _, err = db.Exec(ctx, sql)
	if err != nil {
		return err
	}

	log.Printf("Close migrations sql connection\n")

	return nil
}

func NewConnPool(ctx context.Context) *pgxpool.Pool {
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic("Unable to connect to database")
	}

	return pool
}
