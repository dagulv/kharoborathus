package tagging

import "slices"

func shadowGate(v Vocabulary, targets, keys []string) *bool {
	if len(targets) == 0 {
		for key, state := range v.Tags {
			if state.Gate && state.Enabled && state.Status == statusActive {
				targets = append(targets, key)
			}
		}
	}

	if len(targets) == 0 {
		return nil
	}

	passed := slices.ContainsFunc(targets, func(target string) bool {
		if key, ok := v.Aliases[target]; ok {
			target = key
		}

		return slices.Contains(keys, target)
	})

	return &passed
}
