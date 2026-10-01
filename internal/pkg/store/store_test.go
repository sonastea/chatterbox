package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rs/xid"
	"github.com/sonastea/chatterbox/internal/pkg/database"
)

func TestIntegrationRepositories(t *testing.T) {
	cases := []struct {
		name string
		cfg  database.Config
	}{
		{"sqlite-memory", database.Config{URL: ":memory:"}},
		{"sqlite-file", database.Config{URL: filepath.Join(t.TempDir(), "store.db")}},
		{"postgres", database.Config{Driver: "postgres", URL: os.Getenv("TEST_POSTGRES_URL")}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "postgres" && test.cfg.URL == "" {
				t.Skip("set TEST_POSTGRES_URL or use go run ./tests/integration")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			db, err := database.Open(ctx, test.cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			users, rooms := &UserStore{DB: db}, &RoomStore{DB: db}
			user := User{Xid: xid.New().String(), Name: "same-name", Email: xid.New().String() + "@example.com", Password: "secret"}
			created, err := users.AddUser(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			defer users.RemoveUser(context.Background(), user.Xid)
			if created.Id == 0 || created.Email != user.Email || created.Xid != user.Xid || created.Password != "" {
				t.Fatalf("unexpected stored user: %+v", created)
			}
			found, err := users.FindUserByXid(ctx, user.Xid)
			if err != nil || !reflect.DeepEqual(found, created) {
				t.Fatalf("find user = %+v, error = %v", found, err)
			}
			all, err := users.GetAllUsers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			foundInAll := false
			for _, stored := range all {
				if stored.Xid == user.Xid {
					foundInAll = true
				}
			}
			if !foundInAll {
				t.Fatal("created user missing from all users")
			}
			if _, err := users.AddUser(ctx, user); err == nil {
				t.Fatal("duplicate user should fail")
			}
			if _, err := users.FindUserByXid(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing user error = %v", err)
			}

			room := Room{Xid: xid.New().String(), Name: "test-'" + xid.New().String(), Private: true, Description: "description", Owner_ID: user.Xid}
			storedRoom, err := rooms.AddRoom(ctx, room)
			if err != nil {
				t.Fatal(err)
			}
			defer db.ExecContext(context.Background(), fmt.Sprintf(`DELETE FROM %s WHERE xid = $1`, db.Dialect.Table("Room")), room.Xid)
			room.ID = storedRoom.ID
			if room.ID == 0 || !reflect.DeepEqual(storedRoom, &room) {
				t.Fatalf("stored room = %+v, expected %+v", storedRoom, room)
			}
			byName, err := rooms.FindRoomByName(ctx, room.Name)
			if err != nil || !reflect.DeepEqual(byName, storedRoom) {
				t.Fatalf("find room by name = %+v, error = %v", byName, err)
			}
			byXid, err := rooms.FindRoomByXid(ctx, room.Xid)
			if err != nil || !reflect.DeepEqual(byXid, storedRoom) {
				t.Fatalf("find room by xid = %+v, error = %v", byXid, err)
			}
			if _, err := rooms.FindRoomByName(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing room by name error = %v", err)
			}
			if _, err := rooms.FindRoomByXid(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing room by xid error = %v", err)
			}
			if _, err := rooms.AddRoom(ctx, room); err == nil {
				t.Fatal("duplicate room should fail")
			}
			if err := users.RemoveUser(ctx, user.Xid); err == nil {
				t.Fatal("referenced room owner must not be deleted")
			}
			invalid := Room{Xid: xid.New().String(), Name: xid.New().String(), Owner_ID: "missing"}
			if _, err := rooms.AddRoom(ctx, invalid); err == nil {
				t.Fatal("room with missing owner should fail")
			}
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET description = NULL WHERE xid = $1`, db.Dialect.Table("Room")), room.Xid); err != nil {
				t.Fatal(err)
			}
			if found, err := rooms.FindRoomByXid(ctx, room.Xid); err != nil || found.Description != "" {
				t.Fatalf("nullable description = %+v, error = %v", found, err)
			}

			// Removing by xid must not remove another user with the same display name.
			other := User{Xid: xid.New().String(), Name: user.Name, Email: xid.New().String() + "@example.com"}
			if _, err := users.AddUser(ctx, other); err != nil {
				t.Fatal(err)
			}
			defer users.RemoveUser(context.Background(), other.Xid)
			if err := users.RemoveUser(ctx, other.Xid); err != nil {
				t.Fatal(err)
			}
			if _, err := users.FindUserByXid(ctx, user.Xid); err != nil {
				t.Fatalf("removing same-name user removed owner: %v", err)
			}
			cancelled, stop := context.WithCancel(ctx)
			stop()
			if _, err := rooms.FindRoomByXid(cancelled, room.Xid); !errors.Is(err, context.Canceled) {
				t.Fatalf("query cancellation error = %v", err)
			}
		})
	}
}
