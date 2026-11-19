CREATE TABLE IF NOT EXISTS entities (
  key TEXT PRIMARY KEY, -- English, as a tag key: volvo_cars, solid_state_battery
  name TEXT NOT NULL, -- display name
  kind TEXT NOT NULL, -- company | organization | technology | product | strategy | concept
  status TEXT NOT NULL DEFAULT 'proposed', -- proposed | active
  enabled BOOLEAN NOT NULL DEFAULT true,
  focus BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An alias is never also a key in entities, so lookups take one hop.
CREATE TABLE IF NOT EXISTS entity_aliases (
  alias TEXT PRIMARY KEY,
  key TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (alias <> key)
);

-- How the entity is written in each language. Matching and search use it.
CREATE TABLE IF NOT EXISTS entity_terms (
  key TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  lang TEXT NOT NULL,
  term TEXT NOT NULL,
  PRIMARY KEY (key, lang, term)
);

CREATE INDEX IF NOT EXISTS entity_terms_lower_idx ON entity_terms (lower(term));

CREATE TABLE IF NOT EXISTS entity_tags (
  entity TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  tag TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE,
  PRIMARY KEY (entity, tag)
);

CREATE INDEX IF NOT EXISTS entity_tags_tag_idx ON entity_tags (tag);

CREATE TABLE IF NOT EXISTS post_entities (
  post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  entity TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (post_id, entity)
);

CREATE INDEX IF NOT EXISTS post_entities_entity_idx ON post_entities (entity, created_at DESC);

-- Depth searches repeat the same query text weekly, so only event queries stay unique.
ALTER TABLE discovery_search_queries DROP CONSTRAINT IF EXISTS discovery_search_queries_query_key;
CREATE UNIQUE INDEX IF NOT EXISTS discovery_search_queries_query_idx ON discovery_search_queries (query) WHERE action <> 'depth';

-- Backfill from discovery. Kind is a guess, fixed in the admin.
INSERT INTO entities (key, name, kind)
SELECT DISTINCT normalized_entity, initcap(replace(normalized_entity, '_', ' ')), 'company'
FROM discovery_search_queries
ON CONFLICT DO NOTHING;

INSERT INTO post_entities (post_id, entity, created_at)
SELECT DISTINCT qp.post_id, q.normalized_entity, p.created_at
FROM discovery_search_query_posts qp
JOIN discovery_search_queries q ON q.id = qp.query_id
JOIN posts p ON p.id = qp.post_id
ON CONFLICT DO NOTHING;

INSERT INTO entity_terms (key, lang, term)
SELECT DISTINCT pe.entity, p.lang, e.name
FROM post_entities pe
JOIN entities e ON e.key = pe.entity
JOIN posts p ON p.id = pe.post_id
WHERE p.lang <> ''
ON CONFLICT DO NOTHING;

UPDATE entities SET status = 'active'
WHERE status = 'proposed' AND key IN (
  SELECT pe.entity
  FROM post_entities pe
  JOIN posts p ON p.id = pe.post_id
  JOIN sources s ON s.id = p.source_id
  WHERE s.enabled AND s.status = 'active' AND pe.created_at > now() - interval '30 days'
  GROUP BY pe.entity
  HAVING count(*) >= 3 AND count(DISTINCT p.domain_id) >= 2
);
