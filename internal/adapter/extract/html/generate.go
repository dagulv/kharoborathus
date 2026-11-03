package html

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/port"
)

var (
	ErrNoRule    = errors.New("extract: no rule could be generated")
	ErrAbstained = errors.New("extract: model abstained")
)

var temperatures = []float64{0.2, 0.6, 1.0}

type sample struct {
	URL  string
	Data []byte
}

// The first minSkeletonPage samples are shown to the model, the rest are held out.
func sampleSkeleton(samples []sample) (Skeleton, []sample, error) {
	if need := minSkeletonPage + minHeldOut; len(samples) < need {
		return Skeleton{}, nil, fmt.Errorf("extract: %d sample pages, at least %d needed", len(samples), need)
	}

	shown, heldOut := samples[:minSkeletonPage], samples[minSkeletonPage:]

	docs := make([]document, 0, len(shown))

	for _, p := range shown {
		doc, err := parse(p.Data)
		if err != nil {
			return Skeleton{}, nil, fmt.Errorf("extract: parse %s: %w", p.URL, err)
		}

		docs = append(docs, doc)
	}

	return buildSkeleton(docs), heldOut, nil
}

func (t *htmlExtractor) generateContent(ctx context.Context, l *slog.Logger, samples []sample) (ContentRule, error) {
	skel, heldOut, err := sampleSkeleton(samples)
	if err != nil {
		return ContentRule{}, err
	}

	req := port.Request{
		System: prompts.content,
		Prompt: prompts.user(skel),
		Model:  t.model,
	}

	var (
		best        ContentRule
		bestRemoved float64
		errs        = []error{ErrNoRule}
	)

	for _, temp := range temperatures {
		req.Temperature = &temp

		var rule ContentRule

		if err := t.llm.Unmarshal(ctx, l, req, &rule); err != nil {
			if errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil {
				return ContentRule{}, err
			}

			errs = append(errs, err)
			continue
		}

		rule = rule.sanitize()

		removed, err := checkContentRule(rule, skel, heldOut)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if removed > bestRemoved {
			best, bestRemoved = rule, removed
		}
	}

	if best.Abstained() {
		return ContentRule{}, errors.Join(errs...)
	}

	return best, nil
}

func checkContentRule(rule ContentRule, skel Skeleton, heldOut []sample) (float64, error) {
	if rule.Abstained() {
		return 0, ErrAbstained
	}

	if err := rule.ground(skel.vocab); err != nil {
		return 0, err
	}

	c, err := compile(rule)
	if err != nil {
		return 0, err
	}

	return validate(c, heldOut)
}
