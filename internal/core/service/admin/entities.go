package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

func validEntityKey(key string) bool {
	return key != "" && len(key) <= 200
}

func listEntities(s *postgres.EntityStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		f := domain.AdminEntityFilter{
			Kind:   q.Get("kind"),
			Status: q.Get("status"),
			Search: strings.TrimSpace(q.Get("search")),
		}

		if f.Kind != "" && !slices.Contains(domain.EntityKinds, f.Kind) {
			http.Error(w, domain.ErrUnknownKind.Error(), http.StatusBadRequest)
			return
		}

		if f.Status != "" && f.Status != domain.StatusProposed && f.Status != domain.StatusActive {
			http.Error(w, "unknown status", http.StatusBadRequest)
			return
		}

		if len(f.Search) > 500 {
			http.Error(w, "search too long", http.StatusBadRequest)
			return
		}

		if raw := q.Get("focus"); raw != "" {
			focus, err := strconv.ParseBool(raw)
			if err != nil {
				http.Error(w, "focus must be true or false", http.StatusBadRequest)
				return
			}

			f.Focus = &focus
		}

		page, ok := parsePage(q.Get("page"))
		if !ok {
			http.Error(w, "page must be an integer of at least 1", http.StatusBadRequest)
			return
		}

		f.Page = page

		entities, err := s.ListAdminEntities(r.Context(), f)
		if err != nil {
			serverError(w, l, "list entities", err)
			return
		}

		writeJSON(w, l, entities)
	}
}

func getEntity(s *postgres.EntityStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !validEntityKey(key) {
			http.Error(w, "invalid entity key", http.StatusBadRequest)
			return
		}

		e, err := s.GetAdminEntity(r.Context(), key)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unknown entity "+key, http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "get entity", err)
			return
		}

		writeJSON(w, l, e)
	}
}

// searchIDs gets the depth query that turning focus on creates, once it is committed.
func patchEntity(s *postgres.EntityStore, searchIDs chan<- string, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !validEntityKey(key) {
			http.Error(w, "invalid entity key", http.StatusBadRequest)
			return
		}

		var in domain.EntityPatch
		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		id, err := s.PatchEntity(r.Context(), key, in)
		if errors.Is(err, domain.ErrUnknownKind) || errors.Is(err, domain.ErrEntityName) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unknown entity "+key, http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "patch entity", err)
			return
		}

		if id != "" {
			discovery.Enqueue(searchIDs, l, id)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func setEntityTags(s *postgres.EntityStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !validEntityKey(key) {
			http.Error(w, "invalid entity key", http.StatusBadRequest)
			return
		}

		var in struct {
			Tags []string `json:"tags"`
		}

		if err := readJSON(w, r, &in); err != nil || in.Tags == nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		for i, t := range in.Tags {
			in.Tags[i] = discovery.TagKey(t)
		}

		err := s.SetEntityTags(r.Context(), key, in.Tags)
		if errors.Is(err, domain.ErrUnknownTag) {
			http.Error(w, domain.ErrUnknownTag.Error(), http.StatusBadRequest)
			return
		}

		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unknown entity "+key, http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "set entity tags", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func mergeEntities(s *postgres.EntityStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			From string `json:"from"`
			To   string `json:"to"`
		}

		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		if !validEntityKey(in.From) || !validEntityKey(in.To) {
			http.Error(w, "invalid entity key", http.StatusBadRequest)
			return
		}

		err := s.MergeEntities(r.Context(), in.From, in.To)
		if errors.Is(err, domain.ErrMergeEntitySelf) {
			http.Error(w, domain.ErrMergeEntitySelf.Error(), http.StatusBadRequest)
			return
		}

		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unknown entity", http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "merge entities", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
