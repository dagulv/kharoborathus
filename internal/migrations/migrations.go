package migrations

import (
	"context"
	"embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/tern/v2/migrate"
)

// Tern runs each file through text/template, so SQL must not contain {{.
// A shipped file is never edited. Every schema change is a new numbered file.
//
//go:embed *.sql
var files embed.FS

func Migrate(ctx context.Context, connString string, l *slog.Logger) error {
	return migrateTo(ctx, connString, l, nil)
}

// MigrateTo stops at version, so a test can seed data into an older schema.
func MigrateTo(ctx context.Context, connString string, l *slog.Logger, version int32) error {
	return migrateTo(ctx, connString, l, &version)
}

func migrateTo(ctx context.Context, connString string, l *slog.Logger, target *int32) error {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("err connect: %w", err)
	}
	defer conn.Close(ctx)

	m, err := migrate.NewMigrator(ctx, conn, "public.schema_version")
	if err != nil {
		return fmt.Errorf("err create migrator: %w", err)
	}

	if err = m.LoadMigrations(files); err != nil {
		return fmt.Errorf("err load migrations: %w", err)
	}

	m.OnStart = func(sequence int32, name, direction, _ string) {
		l.Info("migrating", "sequence", sequence, "name", name, "direction", direction)
	}

	run := m.Migrate
	if target != nil {
		run = func(ctx context.Context) error { return m.MigrateTo(ctx, *target) }
	}

	if err = run(ctx); err != nil {
		return fmt.Errorf("err migrate: %w", err)
	}

	version, err := m.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("err get schema version: %w", err)
	}

	l.Info("schema up to date", "version", version)

	return nil
}
