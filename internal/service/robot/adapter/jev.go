package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/config"
)

const (
	jevURL              = "https://api.typesafe.ai/v1/systemone"
	JevModel            = "jev-1.14.0"
	jevTimeout          = time.Minute
	statusJevOverloaded = 529
)

var _ Classifier = jev{}

type jev struct {
	client *http.Client
	url    string
	key    string
	model  string
	l      *slog.Logger
}

func NewJev(env *config.Environment, l *slog.Logger) (Classifier, error) {
	if env.TypesafeAPIKey == "" {
		return nil, errors.New("jev: no api key")
	}

	return jev{
		client: &http.Client{Timeout: jevTimeout},
		url:    jevURL,
		key:    env.TypesafeAPIKey,
		model:  JevModel,
		l:      l,
	}, nil
}

type jevQuestion struct {
	Type string `json:"type"`
	NoulQuestion
}

type jevRequest struct {
	State     any                    `json:"state"`
	Model     string                 `json:"model"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevResponse struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Type string   `json:"type"`
		Noul *float64 `json:"noul"`
	} `json:"answers"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (j jev) Noul(ctx context.Context, state any, questions map[string]NoulQuestion) (map[string]float64, error) {
	if len(questions) == 0 {
		return map[string]float64{}, nil
	}

	req := jevRequest{State: state, Model: j.model, Questions: make(map[string]jevQuestion, len(questions))}

	for id, q := range questions {
		req.Questions[id] = jevQuestion{Type: "noul", NoulQuestion: q}
	}

	var res jevResponse

	if err := j.send(ctx, req, &res); err != nil {
		return nil, err
	}

	j.l.DebugContext(ctx, "jev completion",
		"model", res.Model,
		"questions", len(questions),
		"inputTokens", res.Usage.InputTokens,
		"outputTokens", res.Usage.OutputTokens,
	)

	out := make(map[string]float64, len(questions))

	for id := range questions {
		a, ok := res.Answers[id]
		if !ok || a.Noul == nil {
			return nil, fmt.Errorf("%w: jev has no noul answer for %q", ErrIncomplete, id)
		}

		out[id] = *a.Noul
	}

	return out, nil
}

func (j jev) send(ctx context.Context, body jevRequest, out *jevResponse) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.url, bytes.NewReader(b))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+j.key)
	req.Header.Set("Content-Type", "application/json")

	res, err := j.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(res.Body).Decode(out)
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, statusJevOverloaded:
		return fmt.Errorf("%w: jev %s", ErrUnavailable, res.Status)
	}

	msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))

	return fmt.Errorf("jev: %s: %s", res.Status, bytes.TrimSpace(msg))
}
