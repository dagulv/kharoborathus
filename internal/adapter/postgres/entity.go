package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const (
	maxEntities          = 8
	maxEntityPromptKeys  = 300
	maxEntityName        = 200
	maxWholeWordTermRune = 3
)

type ExtractedEntity struct {
	Key  string   `json:"key"`
	Name string   `json:"name"`
	Term string   `json:"term"`
	Kind string   `json:"kind"`
	Tags []string `json:"tags"`
}

type EntityStore struct {
	db *database.Database
}

func NewEntityStore(db *database.Database) EntityStore {
	return EntityStore{
		db: db,
	}
}

func (s *EntityStore) PromptKeys(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT e.key FROM entities e
		WHERE e.enabled
		ORDER BY e.focus DESC, e.status <> 'active',
			(SELECT count(*) FROM post_entities pe WHERE pe.entity = e.key AND pe.created_at > now() - interval '30 days') DESC,
			e.key
		LIMIT $1
		`, maxEntityPromptKeys)
	if err != nil {
		return nil, fmt.Errorf("err query entity prompt keys: %w", err)
	}

	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan entity prompt keys: %w", err)
	}

	return keys, nil
}

type entityCandidate struct {
	ExtractedEntity
	target string
}

// Returns the linked entities in extraction order, with their status after promotion.
func (s *EntityStore) LinkEntities(ctx context.Context, tx pgx.Tx, post domain.Post, trusted bool, article string, extracted []ExtractedEntity) ([]domain.LinkedEntity, error) {
	// Collapsed like the term, so a double or non-breaking space in the article still matches.
	text := strings.ToLower(strings.Join(strings.Fields(article), " "))
	candidates := make([]entityCandidate, 0, maxEntities)

	for _, e := range extracted[:min(len(extracted), maxEntities)] {
		e.Key = discovery.TagKey(e.Key)
		e.Term = strings.Join(strings.Fields(e.Term), " ")
		e.Kind = strings.ToLower(strings.TrimSpace(e.Kind))

		if strings.Trim(e.Key, "_") == "" || !slices.Contains(domain.EntityKinds, e.Kind) || !grounded(text, strings.ToLower(e.Term)) {
			continue
		}

		e.Name = strings.Join(strings.Fields(e.Name), " ")
		if e.Name == "" {
			e.Name = e.Term
		}

		if utf8.RuneCountInString(e.Name) > maxEntityName {
			e.Name = string([]rune(e.Name)[:maxEntityName])
		}

		// Reads only which key the entity is filed under. Its state is read once that key is locked.
		var target string

		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(
				(SELECT key FROM entities WHERE key = $1),
				(SELECT key FROM entity_aliases WHERE alias = $1),
				(SELECT t.key FROM entity_terms t JOIN entities e ON e.key = t.key WHERE lower(t.term) = lower($2) ORDER BY e.enabled DESC, t.key LIMIT 1),
				$1::text
			)
			`, e.Key, e.Term).Scan(&target); err != nil {
			return nil, fmt.Errorf("err resolve entity %s: %w", e.Key, err)
		}

		candidates = append(candidates, entityCandidate{ExtractedEntity: e, target: target})
	}

	// Locked in key order, so two posts naming the same entities cannot deadlock.
	targets := make([]string, 0, len(candidates))

	for _, c := range candidates {
		targets = append(targets, c.target)
	}

	for _, key := range slices.Compact(slices.Sorted(slices.Values(targets))) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('entity/' || $1))`, key); err != nil {
			return nil, fmt.Errorf("err lock entity %s: %w", key, err)
		}
	}

	var linked []string
	terms := make(map[string]string, len(candidates))
	proposed := make(map[string]bool, len(candidates))

	for _, c := range candidates {
		ok, status, err := s.linkEntity(ctx, tx, post, trusted, c)
		if err != nil {
			return nil, err
		}

		if !ok || slices.Contains(linked, c.target) {
			continue
		}

		linked = append(linked, c.target)
		terms[c.target] = c.Term
		proposed[c.target] = status == domain.StatusProposed
	}

	for _, key := range slices.Sorted(slices.Values(linked)) {
		if !proposed[key] {
			continue
		}

		if _, err := tx.Exec(ctx, `
			UPDATE entities SET status = 'active'
			WHERE key = $1 AND status = 'proposed' AND EXISTS (
				SELECT 1 FROM post_entities pe
				JOIN posts p ON p.id = pe.post_id
				JOIN sources s ON s.id = p.source_id
				WHERE pe.entity = $1 AND s.enabled AND s.status = 'active' AND pe.created_at > now() - interval '30 days'
				HAVING count(*) >= 3 AND count(DISTINCT p.domain_id) >= 2
			)
			`, key); err != nil {
			return nil, fmt.Errorf("err promote entity %s: %w", key, err)
		}
	}

	out := make([]domain.LinkedEntity, 0, len(linked))

	for _, key := range linked {
		e := domain.LinkedEntity{Key: key, Term: terms[key]}

		if err := tx.QueryRow(ctx, `
			SELECT e.status, e.focus,
				array_prepend(lower(e.name), ARRAY(SELECT DISTINCT lower(t.term) FROM entity_terms t WHERE t.key = e.key AND lower(t.term) <> lower(e.name) ORDER BY 1)),
				ARRAY(SELECT a.alias FROM entity_aliases a WHERE a.key = e.key ORDER BY a.alias)
			FROM entities e WHERE e.key = $1
			`, key).Scan(&e.Status, &e.Focus, &e.Names, &e.Aliases); err != nil {
			return nil, fmt.Errorf("err get linked entity %s: %w", key, err)
		}

		out = append(out, e)
	}

	return out, nil
}

// Reports whether the post was linked, and the entity's status before promotion.
func (s *EntityStore) linkEntity(ctx context.Context, tx pgx.Tx, post domain.Post, trusted bool, c entityCandidate) (bool, string, error) {
	if trusted {
		// RETURNING yields a row only when this post creates it, so tags are set only then.
		var created string

		err := tx.QueryRow(ctx, `INSERT INTO entities (key, name, kind) VALUES ($1, $2, $3) ON CONFLICT (key) DO NOTHING RETURNING key`, c.target, c.Name, c.Kind).Scan(&created)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, "", fmt.Errorf("err create entity %s: %w", c.target, err)
		}

		tags := make([]string, 0, len(c.Tags))

		for _, t := range c.Tags {
			tags = append(tags, discovery.TagKey(t))
		}

		if created != "" && len(tags) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO entity_tags (entity, tag)
				SELECT $1, t.key FROM tags t WHERE t.enabled AND t.status = 'active' AND t.key = ANY($2)
				ON CONFLICT DO NOTHING
				`, c.target, tags); err != nil {
				return false, "", fmt.Errorf("err save tags of entity %s: %w", c.target, err)
			}
		}
	}

	var status string
	var enabled bool

	err := tx.QueryRow(ctx, `SELECT status, enabled FROM entities WHERE key = $1`, c.target).Scan(&status, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("err get entity %s: %w", c.target, err)
	}

	if !enabled {
		return false, "", nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO post_entities (post_id, entity) VALUES ($1, $2) ON CONFLICT DO NOTHING`, post.ID, c.target); err != nil {
		return false, "", fmt.Errorf("err link entity %s: %w", c.target, err)
	}

	if post.Lang == "" {
		return true, status, nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO entity_terms (key, lang, term)
		SELECT $1::text, $2::text, $3::text
		WHERE NOT EXISTS (SELECT 1 FROM entity_terms WHERE key = $1 AND lang = $2 AND lower(term) = lower($3))
		`, c.target, post.Lang, c.Term); err != nil {
		return false, "", fmt.Errorf("err save term of entity %s: %w", c.target, err)
	}

	return true, status, nil
}

// Short terms must match a whole word, so AI does not ground in fair.
func grounded(text, term string) bool {
	if term == "" {
		return false
	}

	if utf8.RuneCountInString(term) > maxWholeWordTermRune {
		return strings.Contains(text, term)
	}

	for i := 0; i < len(text); {
		j := strings.Index(text[i:], term)
		if j < 0 {
			return false
		}

		start := i + j
		end := start + len(term)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])

		if !isWordRune(before) && !isWordRune(after) {
			return true
		}

		_, size := utf8.DecodeRuneInString(text[start:])
		i = start + size
	}

	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

const publicEntityQuery = `
	SELECT e.key, e.name, e.kind,
		COALESCE((
			SELECT jsonb_object_agg(g.lang, g.terms)
			FROM (SELECT t.lang, array_agg(t.term ORDER BY t.term) AS terms FROM entity_terms t WHERE t.key = e.key GROUP BY t.lang) g
		), '{}'),
		COALESCE((
			SELECT array_agg(t.key ORDER BY t.key)
			FROM entity_tags et JOIN tags t ON t.key = et.tag
			WHERE et.entity = e.key AND t.enabled AND t.status = 'active'
		), '{}'),
		(SELECT count(*) FROM post_entities pe WHERE pe.entity = e.key AND pe.created_at > now() - interval '30 days') AS strength
	FROM entities e
	WHERE e.enabled AND e.status = 'active'`

func scanPublicEntity(row pgx.CollectableRow) (e domain.PublicEntity, err error) {
	err = row.Scan(&e.Key, &e.Name, &e.Kind, &e.Terms, &e.Tags, &e.Strength)

	return
}

// Tag and category filters count public tags only, like the feed's category filter.
func (s *EntityStore) ListPublic(ctx context.Context, f domain.EntityFilter) ([]domain.PublicEntity, error) {
	rows, err := s.db.Query(ctx, publicEntityQuery+`
		AND ($1 = '' OR e.kind = $1)
		AND ($2 = '' OR EXISTS (
			SELECT 1 FROM entity_tags et JOIN tags t ON t.key = et.tag
			WHERE et.entity = e.key AND t.key = $2 AND t.enabled AND t.status = 'active'
		))
		AND ($3 = '' OR EXISTS (
			SELECT 1 FROM entity_tags et
			JOIN tags t ON t.key = et.tag AND t.enabled AND t.status = 'active'
			JOIN category_tags ct ON ct.tag = et.tag
			JOIN categories c ON c.key = ct.category AND c.enabled
			WHERE et.entity = e.key AND c.key = $3
		))
		ORDER BY strength DESC, e.key
		LIMIT $4 OFFSET $5
		`, f.Kind, f.Tag, f.Category, f.GetLimit(), f.GetOffset())
	if err != nil {
		return nil, fmt.Errorf("err query public entities: %w", err)
	}

	entities, err := pgx.CollectRows(rows, scanPublicEntity)
	if err != nil {
		return nil, fmt.Errorf("err scan public entities: %w", err)
	}

	return entities, nil
}

// An alias gives its target. A proposed or disabled entity is no rows.
func (s *EntityStore) GetPublic(ctx context.Context, key string) (domain.PublicEntity, error) {
	rows, err := s.db.Query(ctx, publicEntityQuery+`
		AND e.key = COALESCE((SELECT a.key FROM entity_aliases a WHERE a.alias = $1), $1)
		`, key)
	if err != nil {
		return domain.PublicEntity{}, fmt.Errorf("err query public entity %s: %w", key, err)
	}

	e, err := pgx.CollectExactlyOneRow(rows, scanPublicEntity)
	if err != nil {
		return domain.PublicEntity{}, fmt.Errorf("err get public entity %s: %w", key, err)
	}

	return e, nil
}
