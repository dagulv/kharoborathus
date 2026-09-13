package feed

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"log"
	"net/http"
)

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /feeds", listFeed)
}

func listFeed(w http.ResponseWriter, r *http.Request) {
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

	for feed, err := range ListFeed(r.Context(), filter) {
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
