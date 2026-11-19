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
		if err := lockEntity(ctx, tx, key); err != nil {
			return nil, err
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

		if err := promoteEntity(ctx, tx, key); err != nil {
			return nil, err
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

func promoteEntity(ctx context.Context, tx pgx.Tx, key string) error {
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
		return fmt.Errorf("err promote entity %s: %w", key, err)
	}

	return nil
}

// Reports whether the post was linked, and the entity's status before promotion.
func (s *EntityStore) linkEntity(ctx context.Context, tx pgx.Tx, post domain.Post, trusted bool, c entityCandidate) (bool, string, error) {
	if trusted {
		// RETURNING yields a row only when this post creates it, so tags are set only then.
		// A merge that committed while this waited for the lock left the target as an alias. It stays one.
		var created string

		err := tx.QueryRow(ctx, `
			INSERT INTO entities (key, name, kind)
			SELECT $1::text, $2::text, $3::text WHERE NOT EXISTS (SELECT 1 FROM entity_aliases WHERE alias = $1)
			ON CONFLICT (key) DO NOTHING
			RETURNING key
			`, c.target, c.Name, c.Kind).Scan(&created)
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

const (
	entityTermsColumn = `
		COALESCE((
			SELECT jsonb_object_agg(g.lang, g.terms)
			FROM (SELECT t.lang, array_agg(t.term ORDER BY t.term) AS terms FROM entity_terms t WHERE t.key = e.key GROUP BY t.lang) g
		), '{}')`

	entityStrengthColumn = `
		(SELECT count(*) FROM post_entities pe WHERE pe.entity = e.key AND pe.created_at > now() - interval '30 days') AS strength`
)

const publicEntityQuery = `
	SELECT e.key, e.name, e.kind,` + entityTermsColumn + `,
		COALESCE((
			SELECT array_agg(t.key ORDER BY t.key)
			FROM entity_tags et JOIN tags t ON t.key = et.tag
			WHERE et.entity = e.key AND t.enabled AND t.status = 'active'
		), '{}'),` + entityStrengthColumn + `
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

const (
	adminEntityColumns = `e.key, e.name, e.kind, e.status, e.enabled, e.focus,` + entityTermsColumn + `,
		COALESCE((SELECT array_agg(et.tag ORDER BY et.tag) FROM entity_tags et WHERE et.entity = e.key), '{}'),` + entityStrengthColumn + `,
		e.created_at`

	adminEntityWhere = `
		($1 = '' OR e.kind = $1)
		AND ($2 = '' OR e.status = $2)
		AND ($3::boolean IS NULL OR e.focus = $3)
		AND ($4 = '' OR e.key ILIKE '%' || $4 || '%' OR e.name ILIKE '%' || $4 || '%'
			OR EXISTS (SELECT 1 FROM entity_terms t WHERE t.key = e.key AND t.term ILIKE '%' || $4 || '%'))`
)

func scanAdminEntity(row pgx.CollectableRow) (e domain.Entity, err error) {
	err = row.Scan(&e.Key, &e.Name, &e.Kind, &e.Status, &e.Enabled, &e.Focus, &e.Terms, &e.Tags, &e.Strength, &e.CreatedAt)

	return
}

func (s *EntityStore) ListAdminEntities(ctx context.Context, f domain.AdminEntityFilter) (domain.EntityPage, error) {
	page := domain.EntityPage{Page: f.Page, PageSize: adminPageSize}
	args := []any{f.Kind, f.Status, f.Focus, f.Search}

	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM entities e WHERE `+adminEntityWhere, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("err count admin entities: %w", err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT `+adminEntityColumns+`
		FROM entities e
		WHERE `+adminEntityWhere+`
		ORDER BY e.focus DESC, strength DESC, e.key
		LIMIT $5 OFFSET $6
		`, append(args, adminPageSize, (f.Page-1)*adminPageSize)...)
	if err != nil {
		return page, fmt.Errorf("err query admin entities: %w", err)
	}

	page.Entities, err = pgx.CollectRows(rows, scanAdminEntity)
	if err != nil {
		return page, fmt.Errorf("err scan admin entities: %w", err)
	}

	return page, nil
}

// An alias is no rows here, so an edit always names the entity itself.
func (s *EntityStore) GetAdminEntity(ctx context.Context, key string) (domain.AdminEntity, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+adminEntityColumns+`,
			ARRAY(SELECT a.alias FROM entity_aliases a WHERE a.key = e.key ORDER BY a.alias),
			(
				SELECT count(*) FROM post_entities pe
				JOIN posts p ON p.id = pe.post_id
				JOIN sources s ON s.id = p.source_id
				WHERE pe.entity = e.key AND s.enabled AND s.status = 'active' AND pe.created_at > now() - interval '30 days'
			)
		FROM entities e
		WHERE e.key = $1
		`, key)
	if err != nil {
		return domain.AdminEntity{}, fmt.Errorf("err query admin entity %s: %w", key, err)
	}

	e, err := pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (e domain.AdminEntity, err error) {
		err = row.Scan(&e.Key, &e.Name, &e.Kind, &e.Status, &e.Enabled, &e.Focus, &e.Terms, &e.Tags, &e.Strength, &e.CreatedAt, &e.Aliases, &e.Support)

		return
	})
	if err != nil {
		return domain.AdminEntity{}, fmt.Errorf("err get admin entity %s: %w", key, err)
	}

	rows, err = s.db.Query(ctx, `
		SELECT id, normalized_entity, action, query, searched_at, result, created_at
		FROM discovery_search_queries
		WHERE normalized_entity = $1
		ORDER BY created_at DESC, id
		LIMIT 20
		`, key)
	if err != nil {
		return domain.AdminEntity{}, fmt.Errorf("err query queries of entity %s: %w", key, err)
	}

	e.Queries, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.DiscoverySearchQuery])
	if err != nil {
		return domain.AdminEntity{}, fmt.Errorf("err scan queries of entity %s: %w", key, err)
	}

	return e, nil
}

func lockEntity(ctx context.Context, tx pgx.Tx, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('entity/' || $1))`, key); err != nil {
		return fmt.Errorf("err lock entity %s: %w", key, err)
	}

	return nil
}

// Returns the id of the depth query that turning focus on created, if any.
func (s *EntityStore) PatchEntity(ctx context.Context, key string, p domain.EntityPatch) (string, error) {
	if p.Kind != nil {
		kind := strings.ToLower(strings.TrimSpace(*p.Kind))
		if !slices.Contains(domain.EntityKinds, kind) {
			return "", domain.ErrUnknownKind
		}

		p.Kind = &kind
	}

	if p.Name != nil {
		name := strings.Join(strings.Fields(*p.Name), " ")
		if name == "" || utf8.RuneCountInString(name) > maxEntityName {
			return "", domain.ErrEntityName
		}

		p.Name = &name
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("err begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockEntity(ctx, tx, key); err != nil {
		return "", err
	}

	var focused bool
	if err := tx.QueryRow(ctx, `SELECT focus FROM entities WHERE key = $1`, key).Scan(&focused); err != nil {
		return "", fmt.Errorf("err get entity %s: %w", key, err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE entities
		SET enabled = COALESCE($2, enabled), focus = COALESCE($3, focus), kind = COALESCE($4, kind), name = COALESCE($5, name)
		WHERE key = $1
		`, key, p.Enabled, p.Focus, p.Kind, p.Name); err != nil {
		return "", fmt.Errorf("err patch entity %s: %w", key, err)
	}

	id := ""

	// Turning focus off leaves a pending depth query to run.
	if p.Focus != nil && *p.Focus && !focused {
		if id, err = queueFocusDepth(ctx, tx, key); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("err commit entity %s: %w", key, err)
	}

	return id, nil
}

// Searches from the newest linked post. Without one, the entity's first trusted post queues the search.
// Runs after the patch's UPDATE, so a patch that disables the entity queues nothing.
func queueFocusDepth(ctx context.Context, tx pgx.Tx, key string) (string, error) {
	var postID, text string

	err := tx.QueryRow(ctx, `
		SELECT pe.post_id, COALESCE(
			(SELECT t.term FROM entity_terms t WHERE t.key = e.key AND t.lang = p.lang AND btrim(t.term) <> '' ORDER BY t.term LIMIT 1),
			e.name
		)
		FROM post_entities pe
		JOIN posts p ON p.id = pe.post_id
		JOIN entities e ON e.key = pe.entity
		WHERE pe.entity = $1 AND e.enabled
		ORDER BY pe.created_at DESC, pe.post_id
		LIMIT 1
		`, key).Scan(&postID, &text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("err get newest post of entity %s: %w", key, err)
	}

	if strings.TrimSpace(text) == "" {
		return "", nil
	}

	id, created, err := discovery.QueueDepth(ctx, tx, key, postID, text)
	if err != nil {
		return "", fmt.Errorf("err queue depth query of entity %s: %w", key, err)
	}

	if !created {
		return "", nil
	}

	return id, nil
}

func (s *EntityStore) SetEntityTags(ctx context.Context, key string, tags []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("err begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM entities WHERE key = $1`, key).Scan(&one); err != nil {
		return fmt.Errorf("err find entity %s: %w", key, err)
	}

	keys := slices.Compact(slices.Sorted(slices.Values(tags)))

	var known int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tags WHERE key = ANY($1)`, keys).Scan(&known); err != nil {
		return fmt.Errorf("err check tags: %w", err)
	}

	if known != len(keys) {
		return domain.ErrUnknownTag
	}

	if _, err := tx.Exec(ctx, `DELETE FROM entity_tags WHERE entity = $1`, key); err != nil {
		return fmt.Errorf("err clear tags of entity %s: %w", key, err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO entity_tags (entity, tag) SELECT $1, unnest($2::text[])`, key, keys); err != nil {
		return fmt.Errorf("err set tags of entity %s: %w", key, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("err commit tags of entity %s: %w", key, err)
	}

	return nil
}

func (s *EntityStore) MergeEntities(ctx context.Context, from, to string) error {
	if from == to {
		return domain.ErrMergeEntitySelf
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("err begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Sorted like LinkEntities, so a merge and a post naming both cannot deadlock.
	for _, key := range slices.Sorted(slices.Values([]string{from, to})) {
		if err := lockEntity(ctx, tx, key); err != nil {
			return err
		}
	}

	var one int

	for _, key := range []string{to, from} {
		if err := tx.QueryRow(ctx, `SELECT 1 FROM entities WHERE key = $1`, key).Scan(&one); err != nil {
			return fmt.Errorf("err find entity %s: %w", key, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO post_entities (post_id, entity, created_at)
		SELECT post_id, $2, created_at FROM post_entities WHERE entity = $1
		ON CONFLICT DO NOTHING
		`, from, to); err != nil {
		return fmt.Errorf("err move posts: %w", err)
	}

	// Lowercase, as LinkEntities compares terms.
	if _, err := tx.Exec(ctx, `
		INSERT INTO entity_terms (key, lang, term)
		SELECT $2, f.lang, f.term FROM entity_terms f
		WHERE f.key = $1 AND NOT EXISTS (SELECT 1 FROM entity_terms t WHERE t.key = $2 AND t.lang = f.lang AND lower(t.term) = lower(f.term))
		ON CONFLICT DO NOTHING
		`, from, to); err != nil {
		return fmt.Errorf("err move terms: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO entity_tags (entity, tag)
		SELECT $2, tag FROM entity_tags WHERE entity = $1
		ON CONFLICT DO NOTHING
		`, from, to); err != nil {
		return fmt.Errorf("err move tags: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE entity_aliases SET key = $2 WHERE key = $1`, from, to); err != nil {
		return fmt.Errorf("err move aliases: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO entity_aliases (alias, key) VALUES ($1, $2)
		ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
		`, from, to); err != nil {
		return fmt.Errorf("err add alias: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE discovery_search_queries SET normalized_entity = $2 WHERE normalized_entity = $1`, from, to); err != nil {
		return fmt.Errorf("err move queries: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE entities SET focus = focus OR (SELECT focus FROM entities WHERE key = $1) WHERE key = $2`, from, to); err != nil {
		return fmt.Errorf("err carry focus: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM entities WHERE key = $1`, from); err != nil {
		return fmt.Errorf("err delete entity: %w", err)
	}

	if err := promoteEntity(ctx, tx, to); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("err commit merge: %w", err)
	}

	return nil
}
