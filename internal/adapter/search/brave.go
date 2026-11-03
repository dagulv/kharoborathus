package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	braveURL     = "https://api.search.brave.com/res/v1/web/search"
	braveTimeout = 20 * time.Second
	braveCount   = 20
)

var errSearchRejected = errors.New("discovery: search rejected")

// Brave answered and billed, but the body could not be read.
var errSearchUnreadable = errors.New("discovery: search unreadable")

type Query struct {
	Q    string
	Lang string
}

type Searcher interface {
	Search(ctx context.Context, l *slog.Logger, q Query) ([]robot.SearchResult, error)
}

type brave struct {
	client *http.Client
	url    string
	key    string
}

func NewBrave(env *config.Environment) (Searcher, error) {
	if env.BraveAPIKey == "" {
		return nil, errors.New("brave: no api key")
	}

	return brave{client: &http.Client{Timeout: braveTimeout}, url: braveURL, key: env.BraveAPIKey}, nil
}

type braveResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets"`
	PageAge       string   `json:"page_age"`
	MetaURL       struct {
		Hostname string `json:"hostname"`
	} `json:"meta_url"`
}

type braveError struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

type braveResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
	News struct {
		Results []braveResult `json:"results"`
	} `json:"news"`
}

func (b brave) Search(ctx context.Context, l *slog.Logger, q Query) ([]robot.SearchResult, error) {
	params := url.Values{
		"q":              {q.Q},
		"country":        {"SE"},
		"freshness":      {"pw"},
		"count":          {strconv.Itoa(braveCount)},
		"result_filter":  {"web,news"},
		"extra_snippets": {"true"},
	}

	if q.Lang == "sv" || q.Lang == "en" {
		params.Set("search_lang", q.Lang)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("err build brave request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)

	res, err := logs.Client(b.client, l, "brave").Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: err call brave: %w", adapter.ErrUnavailable, err)
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= http.StatusInternalServerError:
		return nil, fmt.Errorf("%w: err brave status %s", adapter.ErrUnavailable, res.Status)
	case res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnprocessableEntity:
		var e braveError

		// An empty code just means a rejected query.
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&e)

		// Brave answers a bad key with 422 too, which must stop the worker instead of burning the queue item by item.
		if e.Error.Code == "SUBSCRIPTION_TOKEN_INVALID" {
			return nil, fmt.Errorf("err brave status %s: %s", res.Status, e.Error.Code)
		}

		return nil, fmt.Errorf("%w: err brave status %s", errSearchRejected, res.Status)
	case res.StatusCode != http.StatusOK:
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("err brave status %s: %s", res.Status, bytes.TrimSpace(msg))
	}

	var body braveResponse

	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: err decode brave response: %w", errSearchUnreadable, err)
	}

	var out []robot.SearchResult

	seen := make(map[string]bool)

	for _, r := range slices.Concat(body.Web.Results, body.News.Results) {
		if r.URL == "" || seen[r.URL] {
			continue
		}

		seen[r.URL] = true
		out = append(out, robot.SearchResult{
			URL:           r.URL,
			Title:         r.Title,
			Description:   r.Description,
			ExtraSnippets: r.ExtraSnippets,
			PageAge:       r.PageAge,
			Hostname:      r.MetaURL.Hostname,
		})
	}

	return out, nil
}
