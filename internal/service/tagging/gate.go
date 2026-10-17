package tagging

import (
	"slices"

	"github.com/dagulv/kharoborathus/internal/service/robot"
)

func shadowGate(v Vocabulary, targets, keys []string) *bool {
	live := func(key string) bool {
		state := v.Tags[key]
		return state.Enabled && state.Status == statusActive
	}

	var resolved []string

	for _, target := range targets {
		key := robot.TagKey(target)
		if alias, ok := v.Aliases[key]; ok {
			key = alias
		}

		if live(key) {
			resolved = append(resolved, key)
		}
	}

	// Targets naming no live tag fall back to gate tags, so a typo or a disabled tag never reads as drift.
	if len(resolved) == 0 {
		for key, state := range v.Tags {
			if state.Gate && live(key) {
				resolved = append(resolved, key)
			}
		}
	}

	if len(resolved) == 0 {
		return nil
	}

	passed := slices.ContainsFunc(resolved, func(key string) bool {
		return slices.Contains(keys, key)
	})

	return &passed
}
