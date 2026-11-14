package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestLogRoutes(t *testing.T) {
	mux, db := newMux(t)

	var id int64
	if err := db.QueryRow(context.Background(), `INSERT INTO logs (type, level, request) VALUES ('rss', 'INFO', '{}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want int
	}{
		{"/admin/logs", http.StatusOK},
		{"/admin/logs?type=rss&level=INFO&key=source&value=x&page=2", http.StatusOK},
		{"/admin/logs?type=nope", http.StatusBadRequest},
		{"/admin/logs?level=DEBUG", http.StatusBadRequest},
		{"/admin/logs?key=" + strings.Repeat("k", 101), http.StatusBadRequest},
		{"/admin/logs?key=k&value=" + strings.Repeat("v", 501), http.StatusBadRequest},
		{"/admin/logs?page=0", http.StatusBadRequest},
		{"/admin/logs/abc", http.StatusBadRequest},
		{"/admin/logs/999999", http.StatusNotFound},
		{"/admin/logs/" + strconv.FormatInt(id, 10), http.StatusOK},
	}

	for _, c := range cases {
		if rec := do(mux, http.MethodGet, c.path, ""); rec.Code != c.want {
			t.Errorf("%s: %d %s", c.path, rec.Code, rec.Body)
		}
	}

	rec := do(mux, http.MethodGet, "/admin/logs/"+strconv.FormatInt(id, 10), "")

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if _, ok := got["response"]; !ok || got["response"] != nil || got["request"] == nil || got["id"] != float64(id) {
		t.Errorf("detail: %s", rec.Body)
	}
}
