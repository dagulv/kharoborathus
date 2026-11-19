package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/extract/html"
	"github.com/dagulv/kharoborathus/internal/adapter/fetcher"
	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	adapter "github.com/dagulv/kharoborathus/internal/adapter/robot"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/core/service/admin"
	"github.com/dagulv/kharoborathus/internal/core/service/auth"
	"github.com/dagulv/kharoborathus/internal/core/service/categories"
	"github.com/dagulv/kharoborathus/internal/core/service/enrich"
	"github.com/dagulv/kharoborathus/internal/core/service/posts"
	"github.com/dagulv/kharoborathus/internal/core/service/source"
	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/lib/cors"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/password"
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

	if len(os.Args) > 1 && os.Args[1] == "user" {
		run = addUser
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

func addUser(ctx context.Context) error {
	if len(os.Args) != 4 || os.Args[2] != "add" || strings.TrimSpace(os.Args[3]) == "" {
		fmt.Fprintln(os.Stderr, "usage: kharoborathus user add <email>")
		os.Exit(1)
	}

	email := strings.TrimSpace(os.Args[3])

	env, err := config.Load(true)
	if err != nil {
		return fmt.Errorf("err load config: %w", err)
	}

	db, err := database.NewDatabase(ctx, env.PostgresConn)
	if err != nil {
		return fmt.Errorf("err open database: %w", err)
	}
	defer db.Close()

	// Login has no rate limit, so the password is generated rather than chosen.
	b := make([]byte, 24)
	rand.Read(b)

	pw := base64.RawURLEncoding.EncodeToString(b)

	hash, err := password.Hash(pw)
	if err != nil {
		return fmt.Errorf("err hash password: %w", err)
	}

	users := postgres.NewUserStore(db)
	if err := users.CreateUser(ctx, email, hash); err != nil {
		return err
	}

	fmt.Printf("Added %s, password: %s\n", email, pw)

	return nil
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
	categoryStore := postgres.NewCategoryStore(db)
	sessionStore := postgres.NewSessionStore(db)
	userStore := postgres.NewUserStore(db)
	statsStore := postgres.NewStatsStore(db)
	logStore := postgres.NewLogStore(db)
	discoveryStore := discovery.NewStore(db)
	discoveredStore := postgres.NewDiscoveredStore(db)

	llm, err := adapter.NewDeepSeek(env, l.WithGroup("deepseek"))
	if err != nil {
		return fmt.Errorf("err create llm: %w", err)
	}

	// One pacer for every adapter and the extractor, so two sources on one host still wait for each other.
	pacer := pace.New(source.DomainPace)

	// Domains whose links or pages rule was just saved, so their page sources backfill.
	ruled := make(chan string, source.RuledQueue)

	extractor := html.New(llm, &domainStore, pacer, ruled, l.WithGroup("extract"))

	classifier, err := adapter.NewJev(env, l.WithGroup("jev"))
	if err != nil {
		l.Warn("quality check off, every post that is not a duplicate is accepted", "err", err)
		classifier = nil
	}

	fetchers := map[string]port.Fetcher{
		"rss":  fetcher.NewRSS(pacer),
		"page": fetcher.NewPage(&domainStore, extractor, pacer),
	}

	resultFilter := discovery.NewResultFilter(classifier)
	searcher, searchErr := discovery.NewBrave(env)
	searchIDs := make(chan string, discovery.SearchQueue)
	enrichIDs := make(chan string, enrich.Queue)
	discoveredIDs := make(chan string, source.DiscoveredQueue)

	syncs := make(chan domain.Source, source.SyncQueue)
	backfills := make(chan domain.Source, source.BackfillQueue)
	resyncs := make(chan domain.Resync, source.ResyncQueue)

	sourceService := source.NewService(&sourceStore, &domainStore, &rawPostStore, extractor, classifier, fetcher.NewPages(pacer), enrichIDs, schedule.Always, syncs, calls)

	discovered := source.Discovered{
		Service: &sourceService,
		Store:   &discoveredStore,
	}

	enrichService := enrich.Service{
		DB:            db,
		RawPosts:      &rawPostStore,
		Posts:         &postStore,
		Tags:          &tagStore,
		Categories:    &categoryStore,
		LLM:           llm,
		Classifier:    classifier,
		SearchIDs:     searchIDs,
		DiscoveredIDs: discoveredIDs,
		Calls:         calls,
	}

	mux := http.NewServeMux()
	posts.Routes(mux, &postStore, l.WithGroup("posts"))
	categories.Routes(mux, &categoryStore, l.WithGroup("categories"))

	// The port is public, so every admin route but login sits behind the session cookie.
	protected := http.NewServeMux()
	source.Routes(protected, &sourceStore, syncs, backfills, fetcher.NewProber(l.WithGroup("probe"), calls), l.WithGroup("source"))
	traceStore := postgres.NewTraceStore(db)
	admin.Routes(protected, backfills, resyncs, admin.Stores{Stats: &statsStore, Sources: &sourceStore, Posts: &postStore, Tags: &tagStore, Logs: &logStore, Traces: &traceStore}, l.WithGroup("admin"))

	adminMux := http.NewServeMux()
	auth.Routes(adminMux, protected, &userStore, &sessionStore, l.WithGroup("auth"))
	adminMux.Handle("/admin/", auth.RequireSession(&sessionStore, l.WithGroup("auth"), protected))
	mux.Handle("/admin/", cors.Allow(env.AdminOrigins, auth.RequireJSON(adminMux)))

	// No WriteTimeout: /admin/listing probes a site for up to a minute before it answers.
	server := &http.Server{
		Addr:              env.HttpHost,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("err serve %s: %w", server.Addr, err)
		}

		return nil
	})

	g.Go(func() error {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("err shutdown %s: %w", server.Addr, err)
		}

		return nil
	})

	g.Go(func() error {
		return source.Run(ctx, &sourceService, fetchers, backfills, l.WithGroup("sourced"), calls)
	})

	g.Go(func() error {
		source.BackfillRuled(ctx, &sourceStore, ruled, backfills, l.WithGroup("ruled"))
		return nil
	})

	g.Go(func() error {
		return enrichService.Run(ctx, enrichIDs, l.WithGroup("enrich"))
	})

	g.Go(func() error {
		return source.RunResyncs(ctx, discovered, resyncs, l.WithGroup("resync"))
	})

	g.Go(func() error {
		// Without the quality check every discovered page would be accepted.
		if classifier == nil {
			l.Warn("discovered ingest off", "reason", "no classifier")
			return nil
		}

		return discovered.Run(ctx, discoveredIDs, l.WithGroup("discovered"))
	})

	g.Go(func() error {
		if searchErr != nil {
			l.Warn("search discovery off", "err", searchErr)
			return nil
		}

		// Jev filters the results, and ingest could not fetch the kept ones anyway.
		if classifier == nil {
			l.Warn("search discovery off", "reason", "no classifier")
			return nil
		}

		return discovery.RunSearch(ctx, &discoveryStore, searchIDs, discoveredIDs, searcher, resultFilter, l.WithGroup("search"), calls)
	})

	if err = g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
