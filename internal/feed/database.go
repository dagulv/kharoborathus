package feed

import (
	"context"
	"iter"

	"github.com/jackc/pgx/v5"
)

type database struct {
	conn *pgx.Conn
}

func NewDatabase(ctx context.Context, databaseURL string) (*database, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, nil
	}

	return &database{
		conn: conn,
	}, nil
}

func (d *database) Close(ctx context.Context) {
	d.conn.Close(ctx)
}

func ListFeed(ctx context.Context, filter Filter) iter.Seq2[Feed, error] {
	return func(yield func(Feed, error) bool) {

	}
}
