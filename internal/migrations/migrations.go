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

	if err = m.Migrate(ctx); err != nil {
		return fmt.Errorf("err migrate: %w", err)
	}

	version, err := m.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("err get schema version: %w", err)
	}

	l.Info("schema up to date", "version", version)

	return nil
}
