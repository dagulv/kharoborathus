package tagging

import "slices"

const (
	statusActive  = "active"
	statusDormant = "dormant"
)

type Post struct {
	ID       string
	SourceID string
	Lang     string
	Title    string
	Content  string
}

type Term struct {
	Key  string
	Lang string
	Term string
}

type TagState struct {
	Enabled bool
	Status  string
}

type Vocabulary struct {
	Tags    map[string]TagState
	Aliases map[string]string
}

func (v Vocabulary) ActiveKeys() []string {
	keys := make([]string, 0, len(v.Tags))

	for key, state := range v.Tags {
		if state.Enabled && state.Status == statusActive {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)

	return keys
}
