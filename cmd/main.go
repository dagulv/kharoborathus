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

	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/discovery"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source"
	"github.com/dagulv/kharoborathus/internal/service/tagging"
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
	env, err := config.Load(true)
	if err != nil {
		return
	}

	db, err := database.NewDatabase(ctx, env.PostgresConn)
	if err != nil {
		return
	}

	l := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	feedStore := feed.NewStore(db)
	domainStore := domain.NewStore(db)
	sourceStore := source.NewStore(db, feedStore, domainStore)
	discoveryStore := discovery.NewStore(db)
	taggingStore := tagging.NewStore(db)

	llm, err := adapter.NewDeepSeek(env, l.WithGroup("deepseek"))
	if err != nil {
		l.Error("llm disabled", "err", err)
		return
	}

	trim := source.NewTrim(&domainStore, llm, l.WithGroup("trim"))
	classifier, err := adapter.NewJev(env, l.WithGroup("jev"))
	if err != nil {
		l.Warn("classifier mocked", "err", err)
		classifier = adapter.NewJevMock(l.WithGroup("jev"))
	}

	summarizer := robot.NewSummarizer(llm)
	tagger := robot.NewTagger(classifier)
	tagGenerator := robot.NewTagGenerator(llm)
	discoverer := robot.NewDiscoverer(llm)
	tagMerger := robot.NewTagMerger(classifier)

	syncs := make(chan source.Source, source.SyncQueue)

	mux := http.NewServeMux()
	feed.Routes(mux, feedStore, l.WithGroup("feed"))
	source.Routes(mux, &sourceStore, syncs, l.WithGroup("source"))

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
		return source.Run(ctx, &sourceStore, syncs, trim, summarizer, schedule.Always, l.WithGroup("sourced"))
	})

	g.Go(func() error {
		return tagging.Run(ctx, &taggingStore, tagGenerator, tagger, l.WithGroup("tagging"))
	})

	g.Go(func() error {
		return tagging.RunPromotion(ctx, &taggingStore, tagMerger, tagger, l.WithGroup("promotion"))
	})

	g.Go(func() error {
		return tagging.RunMaintenance(ctx, &taggingStore, l.WithGroup("maintenance"))
	})

	g.Go(func() error {
		return discovery.Run(ctx, &discoveryStore, discoverer, llm.Schedule(), l.WithGroup("discovery"))
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
