package feed

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"log"
	"net/http"
)

// Routes registers the feed endpoints on mux.
func Routes(mux *http.ServeMux, db *database) {
	mux.HandleFunc("GET /feeds", listFeed(db))
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

		// Stream each feed straight to the response as it is yielded, so the
		// full result set is never held in memory. The status is already
		// committed once the first bytes are written, so a later failure can
		// only cut the response short.
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
