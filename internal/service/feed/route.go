package feed

import (
	"database/sql"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"log"
	"log/slog"
	"net/http"

	"github.com/dagulv/palantir/internal/lib/query"
)

func Routes(mux *http.ServeMux, s Store, l *slog.Logger) {
	mux.HandleFunc("GET /feeds", listFeed(s, l))
	mux.HandleFunc("GET /feeds/{id}", getFeed(s, l))
}

func listFeed(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		filter := query.Filter{
			Limit:   q.Get("limit"),
			Offset:  q.Get("offset"),
			Order:   q.Get("order"),
			OrderBy: q.Get("orderBy"),
			Search:  q.Get("search"),
		}

		w.Header().Set("Content-Type", "application/json")

		enc := jsontext.NewEncoder(w)

		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			log.Printf("feed: write response: %v", err)
			return
		}

		for feed, err := range s.ListFeeds(r.Context(), filter) {
			if err != nil {
				log.Printf("feed: list feeds: %v", err)
				return
			}

			if err := json.MarshalEncode(enc, feed); err != nil {
				log.Printf("feed: write response: %v", err)
				return
			}
		}

		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			log.Printf("feed: write response: %v", err)
		}
	}
}

func getFeed(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feed, err := s.GetFeed(r.Context(), r.PathValue("id"))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}

			log.Printf("feed error: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if err := json.MarshalWrite(w, feed); err != nil {
			log.Printf("feed: write response: %v", err)
		}
	}
}
