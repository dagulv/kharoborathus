package html

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	adapter "github.com/dagulv/kharoborathus/internal/adapter/robot"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
)

const fetchTimeout = 20 * time.Second

type DomainStore interface {
	SetExtractRule(ctx context.Context, id, key string, rule json.RawMessage) error
}

var _ port.Extractor = (*htmlExtractor)(nil)

type htmlExtractor struct {
	llm     port.LLM
	model   string
	domains DomainStore
	client  *http.Client
	l       *slog.Logger

	generating chan struct{}

	mu        sync.Mutex
	samples   map[string][]sample
	sampledAt map[string]uint64
	sampleSeq uint64
	requested map[string]map[field]bool
	failedAt  map[field]map[string]time.Time
	listings  map[string][]sample
	listedAt  map[string]time.Time
	triedOne  map[string]bool
}

func New(llm port.LLM, domains DomainStore, l *slog.Logger) port.Extractor {
	return newExtractor(llm, domains, l)
}

func newExtractor(llm port.LLM, domains DomainStore, l *slog.Logger) *htmlExtractor {
	return &htmlExtractor{
		llm:        llm,
		model:      adapter.ModelFlash,
		domains:    domains,
		client:     safehttp.NewClient(fetchTimeout),
		l:          l,
		generating: make(chan struct{}, 1),
		samples:    make(map[string][]sample),
		sampledAt:  make(map[string]uint64),
		requested:  make(map[string]map[field]bool),
		failedAt:   make(map[field]map[string]time.Time),
		listings:   make(map[string][]sample),
		listedAt:   make(map[string]time.Time),
		triedOne:   make(map[string]bool),
	}
}

func (t *htmlExtractor) Content(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (string, error) {
	rule, ok := ruleOf[ContentRule](d, fieldContent)
	if !ok {
		t.collect(ctx, l, d, fieldContent, sample{URL: url, Data: page})
	}

	title, _ := ruleOf[FieldRule](d, fieldTitle)

	e, err := content(rule, title, page)
	if err != nil {
		return "", fmt.Errorf("err content %s: %w", url, err)
	}

	if !e.OK() {
		return "", fmt.Errorf("err content checks %s: %v", url, e.Failures)
	}

	return e.Content, nil
}

func (t *htmlExtractor) Title(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (string, error) {
	rule := t.fieldRule(ctx, l, d, fieldTitle, url, page)

	doc, err := parse(page)
	if err != nil {
		return "", fmt.Errorf("err parse %s: %w", url, err)
	}

	return readTitle(doc, rule), nil
}

func (t *htmlExtractor) Authors(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) ([]string, error) {
	rule := t.fieldRule(ctx, l, d, fieldAuthors, url, page)

	doc, err := parse(page)
	if err != nil {
		return nil, fmt.Errorf("err parse %s: %w", url, err)
	}

	return readAuthors(doc, rule), nil
}

func (t *htmlExtractor) PostedAt(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (time.Time, error) {
	rule := t.fieldRule(ctx, l, d, fieldPostedAt, url, page)

	doc, err := parse(page)
	if err != nil {
		return time.Time{}, fmt.Errorf("err parse %s: %w", url, err)
	}

	return readPostedAt(doc, rule), nil
}

func (t *htmlExtractor) fieldRule(ctx context.Context, l *slog.Logger, d domain.Domain, f field, url string, page []byte) FieldRule {
	rule, ok := ruleOf[FieldRule](d, f)
	if !ok {
		t.collect(ctx, l, d, f, sample{URL: url, Data: page})
	}

	return rule
}

// An unreadable rule counts as missing, so a new one is generated.
func ruleOf[R any](d domain.Domain, f field) (rule R, ok bool) {
	raw, ok := d.Metadata.Extract[string(f)]
	if !ok {
		return rule, false
	}

	if err := json.Unmarshal(raw, &rule); err != nil {
		var zero R
		return zero, false
	}

	return rule, true
}

func (t *htmlExtractor) saveRule(ctx context.Context, domainID string, f field, rule any) error {
	raw, err := json.Marshal(rule)
	if err != nil {
		return fmt.Errorf("err marshal %s rule: %w", f, err)
	}

	return t.domains.SetExtractRule(ctx, domainID, string(f), raw)
}
