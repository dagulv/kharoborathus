package html

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
)

var ErrAbstained = errors.New("trimmer: model abstained")

var temperatures = []float64{0.2, 0.6, 1.0}

func (t *htmlTrimmer) GenerateRuleset(ctx context.Context, l *slog.Logger, samples []trimmer.Sample) (Rule, error) {
	if need := minSkeletonPage + minHeldOut; len(samples) < need {
		return Rule{}, fmt.Errorf("trimmer: %d sample pages, at least %d needed", len(samples), need)
	}

	shown, heldOut := samples[:minSkeletonPage], samples[minSkeletonPage:]

	docs := make([]document, 0, len(shown))

	for _, p := range shown {
		doc, err := parse(p.Data)
		if err != nil {
			return Rule{}, fmt.Errorf("trimmer: parse %s: %w", p.URL, err)
		}

		docs = append(docs, doc)
	}

	skel := buildSkeleton(docs)

	req := adapter.Request{
		System: prompts.system,
		Prompt: prompts.user(skel),
		Model:  t.model,
	}

	var (
		best        Rule
		bestRemoved float64
		errs        = []error{trimmer.ErrNoRule}
	)

	for _, temp := range temperatures {
		req.Temperature = &temp

		var rule Rule

		if err := t.llm.Unmarshal(ctx, l, req, &rule); err != nil {
			if errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil {
				return Rule{}, err
			}

			errs = append(errs, err)
			continue
		}

		rule = rule.sanitize()

		removed, err := t.check(rule, skel, heldOut)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if removed > bestRemoved {
			best, bestRemoved = rule, removed
		}
	}

	if best.Abstained() {
		return Rule{}, errors.Join(errs...)
	}

	return best, nil
}

func (t *htmlTrimmer) check(rule Rule, skel Skeleton, heldOut []trimmer.Sample) (float64, error) {
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
