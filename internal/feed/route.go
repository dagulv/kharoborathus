package feed

import (
	"database/sql"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"log"
	"net/http"
)

func Routes(mux *http.ServeMux, db *database) {
	mux.HandleFunc("GET /feeds", listFeed(db))
	mux.HandleFunc("GET /feeds/{id}", getFeed(db))
}

func listFeed(db *database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		filter := Filter{
			Limit:   query.Get("limit"),
			Offset:  query.Get("offset"),
			Order:   query.Get("order"),
			OrderBy: query.Get("orderBy"),
			Search:  query.Get("search"),
		}

		w.Header().Set("Content-Type", "application/json")

		enc := jsontext.NewEncoder(w)

		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			log.Printf("feed: write response: %v", err)
			return
		}

		for feed, err := range db.ListFeed(r.Context(), filter) {
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

func getFeed(db *database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feed, err := db.GetFeed(r.Context(), r.PathValue("id"))
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
