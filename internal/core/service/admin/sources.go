package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

func getStats(s *postgres.StatsStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := s.Stats(r.Context())
		if err != nil {
			serverError(w, l, "get stats", err)
			return
		}

		writeJSON(w, l, stats)
	}
}

func listSources(s *postgres.SourceStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sources, err := s.ListAdminSources(r.Context(), r.URL.Query().Get("deleted") == "true")
		if err != nil {
			serverError(w, l, "list sources", err)
			return
		}

		writeJSON(w, l, sources)
	}
}

// The messages match the old web admin's form.
func validateHTTPURL(raw string) string {
	if raw == "" {
		return "URL is required"
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "Must be a valid URL"
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "Only http(s) URLs are supported"
	}

	return ""
}

func validateNewSource(in domain.NewSource) string {
	if msg := validateHTTPURL(in.URL); msg != "" {
		return msg
	}

	if in.Adapter != "rss" && in.Adapter != "page" {
		return "Check the URL first"
	}

	if msg := validateHTTPURL(in.ListingURL); msg != "" {
		return msg
	}

	if in.Rate < 1 {
		return "Rate must be at least 1"
	}

	if in.Lang != "" && in.Lang != "sv" && in.Lang != "en" {
		return "Unknown language"
	}

	return ""
}

// The source is saved either way. The Backfill button covers a backfill that could not be queued.
func queueBackfill(r *http.Request, s *postgres.SourceStore, backfills chan<- domain.Source, id string, l *slog.Logger) {
	source, err := s.GetSource(r.Context(), id)
	if err != nil {
		l.Error("queue backfill", "source", id, "err", err)
		return
	}

	select {
	case backfills <- source:
	default:
		l.Warn("backfill queue full", "source", id)
	}
}

func createSource(s *postgres.SourceStore, backfills chan<- domain.Source, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in domain.NewSource
		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		in.URL = strings.TrimSpace(in.URL)

		if msg := validateNewSource(in); msg != "" {
			http.Error(w, msg, http.StatusBadRequest)
			return
		}

		id, err := s.CreateSource(r.Context(), in)
		if errors.Is(err, domain.ErrSourceExists) {
			http.Error(w, "A source with this feed or page already exists", http.StatusConflict)
			return
		}

		if err != nil {
			serverError(w, l, "create source", err)
			return
		}

		queueBackfill(r, s, backfills, id, l)

		w.WriteHeader(http.StatusCreated)
	}
}

func reviewSource(s *postgres.SourceStore, backfills chan<- domain.Source, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled bool `json:"enabled"`
		}

		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		was, err := s.ReviewSource(r.Context(), r.PathValue("id"), in.Enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Source not found", http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "review source", err)
			return
		}

		// Approving a proposed source is its first scrape, so it backfills.
		if in.Enabled && was == domain.StatusProposed {
			queueBackfill(r, s, backfills, r.PathValue("id"), l)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
