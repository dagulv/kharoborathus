package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
)

type idleLLM struct{}

func (idleLLM) Schedule() schedule.Schedule { return schedule.Always }

func (idleLLM) Prompt(context.Context, adapter.Request) (string, error) {
	return "", adapter.ErrUnavailable
}

func (idleLLM) Unmarshal(context.Context, adapter.Request, any) error {
	return adapter.ErrUnavailable
}

func TestCollectEvictsOldestDomain(t *testing.T) {
	tr := NewTrim(nil, idleLLM{}, slog.New(slog.DiscardHandler))

	for i := range maxSampleDomains + 1 {
		tr.collect(context.Background(), domain.Domain{ID: strconv.Itoa(i)}, trimmer.Sample{URL: fmt.Sprintf("https://d%d.example/a", i)})
	}

	// A new sample for domain 1 makes domain 2 the oldest.
	tr.collect(context.Background(), domain.Domain{ID: "1"}, trimmer.Sample{URL: "https://d1.example/b"})
	tr.collect(context.Background(), domain.Domain{ID: "new"}, trimmer.Sample{URL: "https://new.example/a"})

	if len(tr.samples) != maxSampleDomains {
		t.Errorf("%d domains sampled", len(tr.samples))
	}

	for id, want := range map[string]bool{"0": false, "1": true, "2": false, "3": true, "new": true} {
		if _, ok := tr.samples[id]; ok != want {
			t.Errorf("domain %s kept %v", id, ok)
		}
	}
}

func TestTrimClientRefusesPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private address fetched")
	}))
	defer srv.Close()

	if _, err := NewTrim(nil, idleLLM{}, slog.New(slog.DiscardHandler)).client.Get(srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("err %v", err)
	}
}
