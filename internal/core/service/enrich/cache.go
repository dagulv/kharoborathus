package enrich

import (
	"context"
	"sync"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

// Tags change only on promotion or admin edits, so a minute of staleness saves two queries per post.
const tagCacheTTL = time.Minute

type tagSet struct {
	keys   []string
	active []domain.Tag
}

type cachedTags struct {
	set tagSet
	at  time.Time
}

type tagCache struct {
	mu   sync.Mutex
	now  func() time.Time
	sets map[string]cachedTags
}

func (c *tagCache) get(ctx context.Context, lang string, load func(context.Context, string) (tagSet, error)) (tagSet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now
	if c.now != nil {
		now = c.now
	}

	if cached, ok := c.sets[lang]; ok && now().Sub(cached.at) < tagCacheTTL {
		return cached.set, nil
	}

	set, err := load(ctx, lang)
	if err != nil {
		return tagSet{}, err
	}

	if c.sets == nil {
		c.sets = make(map[string]cachedTags)
	}

	c.sets[lang] = cachedTags{set: set, at: now()}

	return set, nil
}
