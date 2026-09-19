package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagulv/palantir/internal/lib/config"
	"github.com/dagulv/palantir/internal/lib/database"
	"github.com/dagulv/palantir/internal/service/feed"
	"github.com/dagulv/palantir/internal/service/source"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := app(ctx); err != nil {
		log.Fatal(err)
	}
}

func app(ctx context.Context) (err error) {
	env, err := config.Load()
	if err != nil {
		return
	}

	db, err := database.NewDatabase(ctx, env.PostgresConn)
	if err != nil {
		return
	}

	feedStore := feed.NewStore(db)
	sourceStore := source.NewStore(db, feedStore)

	l := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mux := http.NewServeMux()
	feed.Routes(mux, feedStore, l.WithGroup("feed"))

	server := &http.Server{
		Addr:    env.HttpHost,
		Handler: mux,
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	})

	g.Go(func() error {
		return source.Run(ctx, &sourceStore, l.WithGroup("sourced"))
	})

	g.Go(func() error {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		return server.Shutdown(shutdownCtx)
	})

	if err = g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
