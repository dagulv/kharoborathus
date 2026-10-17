package switches

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

const (
	ShadowGate       = "shadow_gate"
	DiscoveredIngest = "discovered_ingest"
	SearchDiscovery  = "search_discovery"
	SourcePromotion  = "source_promotion"
)

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{db: db}
}

func (s *Store) Enabled(ctx context.Context, key string) (bool, error) {
	var on bool

	if err := s.db.QueryRow(ctx, `
		SELECT coalesce((SELECT enabled FROM switches WHERE key = $1), false)
		`, key).Scan(&on); err != nil {
		return false, fmt.Errorf("err get switch %s: %w", key, err)
	}

	return on, nil
}
