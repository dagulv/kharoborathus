package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

var logLevels = []string{"INFO", "WARN", "ERROR"}

func listLogs(s *postgres.LogStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		f := domain.LogFilter{
			Type:      q.Get("type"),
			Level:     q.Get("level"),
			MetaKey:   strings.TrimSpace(q.Get("key")),
			MetaValue: strings.TrimSpace(q.Get("value")),
		}

		if f.Type != "" && !slices.Contains(domain.LogTypes, f.Type) {
			http.Error(w, "unknown log type", http.StatusBadRequest)
			return
		}

		if f.Level != "" && !slices.Contains(logLevels, f.Level) {
			http.Error(w, "unknown log level", http.StatusBadRequest)
			return
		}

		if len(f.MetaKey) > 100 || len(f.MetaValue) > 500 {
			http.Error(w, "metadata key or value too long", http.StatusBadRequest)
			return
		}

		page, ok := parsePage(q.Get("page"))
		if !ok {
			http.Error(w, "page must be an integer of at least 1", http.StatusBadRequest)
			return
		}

		f.Page = page

		logs, err := s.ListLogs(r.Context(), f)
		if err != nil {
			serverError(w, l, "list logs", err)
			return
		}

		writeJSON(w, l, logs)
	}
}

func getLog(s *postgres.LogStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "log id must be a number", http.StatusBadRequest)
			return
		}

		log, err := s.GetLog(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Log not found", http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "get log", err)
			return
		}

		writeJSON(w, l, log)
	}
}
