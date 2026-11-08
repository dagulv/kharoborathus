// Package dbtest gives tests a fresh, migrated database. Tests skip unless TEST_POSTGRES_CONN is set.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/migrations"
	"github.com/jackc/pgx/v5"
)

// Needs a postgres with the Swedish dictionary files, such as the infra/db image.
func New(t *testing.T) *database.Database {
	t.Helper()

	conn := os.Getenv("TEST_POSTGRES_CONN")
	if conn == "" {
		t.Skip("TEST_POSTGRES_CONN not set")
	}

	ctx := context.Background()

	admin, err := pgx.Connect(ctx, conn)
	if err != nil {
		t.Fatalf("err connect admin: %v", err)
	}
	defer admin.Close(ctx)

	name := "kharo_test_" + RandomHex(t)

	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("err create database: %v", err)
	}

	u, err := url.Parse(conn)
	if err != nil {
		t.Fatalf("err parse conn: %v", err)
	}

	u.Path = "/" + name

	if err := migrations.Migrate(ctx, u.String(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("err migrate: %v", err)
	}

	db, err := database.NewDatabase(ctx, u.String())
	if err != nil {
		t.Fatalf("err open database: %v", err)
	}

	t.Cleanup(func() {
		db.Close()

		admin, err := pgx.Connect(ctx, conn)
		if err != nil {
			t.Errorf("err connect admin: %v", err)
			return
		}
		defer admin.Close(ctx)

		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("err drop database: %v", err)
		}
	})

	return db
}

func RandomHex(t *testing.T) string {
	b := make([]byte, 6)

	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}

	return hex.EncodeToString(b)
}
