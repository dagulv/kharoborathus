package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/extract/html"
	"github.com/dagulv/kharoborathus/internal/adapter/fetcher"
	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	adapter "github.com/dagulv/kharoborathus/internal/adapter/robot"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/service/source"
	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
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

	feedStore := feed.NewStore(db)
	domainStore := postgres.NewStore(db)
	sourceStore := postgres.NewSourceStore(db)
	rawPostStore := postgres.NewRawPostStore(db)
	discoveryStore := discovery.NewStore(db)
	taggingStore := tagging.NewStore(db)
	ingestStore := ingest.NewStore(db)

	llm, err := adapter.NewDeepSeek(env, l.WithGroup("deepseek"))
	if err != nil {
		return fmt.Errorf("err create llm: %w", err)
	}

	extractor := html.New(llm, &domainStore, l.WithGroup("extract"))
	classifier, err := adapter.NewJev(env, l.WithGroup("jev"))
	mocked := err != nil
	if mocked {
		l.Warn("classifier mocked", "err", err)
		classifier = adapter.NewJevMock(l.WithGroup("jev"))
	}

	summarizer := robot.NewSummarizer(llm)
	tagger := robot.NewTagger(classifier)
	tagGenerator := robot.NewTagGenerator(llm)
	discoverer := robot.NewDiscoverer(llm)
	tagMerger := robot.NewTagMerger(classifier)
	resultFilter := robot.NewResultFilter(llm)
	searcher, searchErr := discovery.NewBrave(env)
	ingester := ingest.NewIngester(&ingestStore, &taggingStore, &feedStore, &domainStore, extractor, tagger, l.WithGroup("ingest"), calls)

	syncs := make(chan domain.Source, source.SyncQueue)

	mux := http.NewServeMux()
	feed.Routes(mux, feedStore, l.WithGroup("feed"))

	// The proxy publishes only the public port, so sync and listing stay reachable from the admin alone.
	internalMux := http.NewServeMux()
	source.Routes(internalMux, &sourceStore, syncs, l.WithGroup("source"), calls)

	servers := []*http.Server{
		{Addr: env.HttpHost, Handler: mux},
		{Addr: env.InternalHttpHost, Handler: internalMux},
	}

	sourceService := source.NewService(&sourceStore, &domainStore, &rawPostStore, extractor, schedule.Always, syncs)

	fetchers := map[string]source.Fetcher{
		"rss":       source.NewFetcher(rss.New(pacer, l), calls),
		"page":      source.NewFetcher(fetcher.NewPage(&rawPostStore, &domainStore, extractor, pacer, l), calls),
		"wordpress": source.NewFetcher(wordpress.New(pacer, l), calls),
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, server := range servers {
		g.Go(func() error {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("err serve %s: %w", server.Addr, err)
			}

			return nil
		})
	}

	g.Go(func() error {
		return sourceService.Run(ctx, l.WithGroup("sourced"))
	})

	g.Go(func() error {
		return discovery.Run(ctx, &discoveryStore, discoverer, llm.Schedule(), l.WithGroup("discovery"), calls)
	})

	g.Go(func() error {
		if searchErr != nil {
			l.Warn("search discovery off", "err", searchErr)
			return nil
		}

		if mocked {
			l.Warn("search discovery off, discovered ingest cannot run with the mocked classifier")
			return nil
		}

		return discovery.RunSearch(ctx, &discoveryStore, &switchStore, searcher, resultFilter, llm.Schedule(), l.WithGroup("search"), calls)
	})

	g.Go(func() error {
		return ingest.RunExtraction(ctx, &ingestStore, l.WithGroup("links"))
	})

	g.Go(func() error {
		if mocked {
			l.Warn("discovered ingest off, the mocked classifier passes almost any page")
			return nil
		}

		return ingester.Run(ctx)
	})

	g.Go(func() error {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		var errs []error
		for _, server := range servers {
			if err := server.Shutdown(shutdownCtx); err != nil {
				errs = append(errs, fmt.Errorf("err shutdown %s: %w", server.Addr, err))
			}
		}

		return errors.Join(errs...)
	})

	if err = g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
