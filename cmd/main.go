package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dagulv/kharoborathus/internal/adapter/extract/html"
	"github.com/dagulv/kharoborathus/internal/adapter/fetcher"
	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	adapter "github.com/dagulv/kharoborathus/internal/adapter/robot"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/service/enrich"
	"github.com/dagulv/kharoborathus/internal/core/service/source"
	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/migrations"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	run := app
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		run = migrate
	}

	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func migrate(ctx context.Context) error {
	env, err := config.Load(true)
	if err != nil {
		return fmt.Errorf("err load config: %w", err)
	}

	l := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	return migrations.Migrate(ctx, env.PostgresConn, l.WithGroup("migrate"))
}

func app(ctx context.Context) (err error) {
	env, err := config.Load(true)
	if err != nil {
		return
	}

	db, err := database.NewDatabase(ctx, env.PostgresConn)
	if err != nil {
		return
	}

	l := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Third-party requests, one row each in the logs table.
	calls := slog.New(logs.NewHandler(db, l.WithGroup("logs")))

	domainStore := postgres.NewStore(db)
	sourceStore := postgres.NewSourceStore(db)
	rawPostStore := postgres.NewRawPostStore(db)
	postStore := postgres.NewPostStore(db)
	tagStore := postgres.NewTagStore(db)
	discoveryStore := discovery.NewStore(db)

	llm, err := adapter.NewDeepSeek(env, l.WithGroup("deepseek"))
	if err != nil {
		return fmt.Errorf("err create llm: %w", err)
	}

	extractor := html.New(llm, &domainStore, l.WithGroup("extract"))

	classifier, err := adapter.NewJev(env, l.WithGroup("jev"))
	if err != nil {
		l.Warn("quality check off, every post that is not a duplicate is accepted", "err", err)
		classifier = nil
	}

	// One pacer for every adapter, so two sources on one host still wait for each other.
	pacer := pace.New(source.DomainPace)

	fetchers := map[string]source.Fetcher{
		"rss":       source.NewFetcher(fetcher.NewRSS(&rawPostStore, pacer, l), calls),
		"page":      source.NewFetcher(fetcher.NewPage(&rawPostStore, &domainStore, extractor, pacer, l), calls),
		"wordpress": source.NewFetcher(fetcher.NewWordPress(&rawPostStore, pacer, l), calls),
	}

	resultFilter := discovery.NewResultFilter(llm)
	searcher, searchErr := discovery.NewBrave(env)
	searchIDs := make(chan string, discovery.SearchQueue)
	enrichIDs := make(chan string, enrich.Queue)

	sourceService := source.NewService(&sourceStore, &domainStore, &rawPostStore, extractor, classifier, enrichIDs, schedule.Always, nil)

	enrichService := enrich.Service{
		DB:         db,
		RawPosts:   &rawPostStore,
		Posts:      &postStore,
		Tags:       &tagStore,
		LLM:        llm,
		Classifier: classifier,
		SearchIDs:  searchIDs,
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return source.Run(ctx, &sourceService, fetchers, l.WithGroup("sourced"))
	})

	g.Go(func() error {
		return enrichService.Run(ctx, enrichIDs, l.WithGroup("enrich"))
	})

	g.Go(func() error {
		if searchErr != nil {
			l.Warn("search discovery off", "err", searchErr)
			return nil
		}

		return discovery.RunSearch(ctx, &discoveryStore, searchIDs, searcher, resultFilter, llm.Schedule(), l.WithGroup("search"), calls)
	})

	if err = g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
