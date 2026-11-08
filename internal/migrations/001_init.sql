CREATE TEXT SEARCH DICTIONARY swedish_dict (
  TEMPLATE = ispell,
  DictFile = sv_se,
  AffFile = sv_se,
  StopWords = swedish
);

CREATE TEXT SEARCH CONFIGURATION ts_swedish (
  COPY = pg_catalog.swedish
);

ALTER TEXT SEARCH CONFIGURATION ts_swedish
ALTER MAPPING FOR asciiword,
word,
hword,
hword_part,
asciihword,
hword_asciipart
WITH
  swedish_dict,
  swedish_stem;

CREATE TABLE IF NOT EXISTS tags (
  key TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL DEFAULT true,
  status TEXT NOT NULL DEFAULT 'active', -- proposed | active
  type TEXT NOT NULL DEFAULT 'quality', -- sentiment | quality etc
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- TODO: Correctly handle tag aliases here.
CREATE TABLE IF NOT EXISTS tag_terms (
  key TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE,
  lang TEXT NOT NULL,
  term TEXT NOT NULL,
  matcher tsquery GENERATED ALWAYS AS (
    CASE lang
      WHEN 'sv' THEN phraseto_tsquery('ts_swedish', term)
      WHEN 'en' THEN phraseto_tsquery('english', term)
      ELSE phraseto_tsquery('simple', term)
    END
  ) STORED,
  PRIMARY KEY (key, lang, term)
);

CREATE TABLE IF NOT EXISTS domains (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  base_url TEXT UNIQUE NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sources (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  domain_id UUID NOT NULL REFERENCES domains (id),
  adapter TEXT NOT NULL,
  url TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT false,
  rate INT NOT NULL DEFAULT 15,
  metadata JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'active', -- proposed | active | flagged
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ,
  CHECK (deleted_at IS NULL OR NOT enabled)
);

CREATE INDEX IF NOT EXISTS sources_url_idx ON sources (url);

CREATE INDEX IF NOT EXISTS sources_enabled_idx ON sources (enabled);

CREATE TABLE IF NOT EXISTS raw_posts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id UUID NOT NULL REFERENCES sources (id),
  url TEXT UNIQUE NOT NULL,
  lang TEXT NOT NULL,
  domain_id UUID NOT NULL REFERENCES domains (id),
  raw_content TEXT NOT NULL,
  content TEXT NOT NULL,
  meta_content JSON NOT NULL DEFAULT '{}',
  simhash BIGINT,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | rejected | duplicate | failed
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS posts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id UUID NOT NULL REFERENCES sources (id),
  url TEXT UNIQUE NOT NULL,
  lang TEXT NOT NULL,
  domain_id UUID NOT NULL REFERENCES domains (id),
  title TEXT NOT NULL,
  short_content TEXT NOT NULL,
  content TEXT NOT NULL,
  tags TEXT[] NOT NULL DEFAULT '{}',
  posted_at TIMESTAMPTZ,
  authors TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  simhash BIGINT,
  ts tsvector GENERATED ALWAYS AS (
    CASE lang
      WHEN 'sv' THEN setweight(to_tsvector('ts_swedish', title), 'A') || setweight(
        to_tsvector('ts_swedish', coalesce(short_content, '')),
        'B'
      )
      WHEN 'en' THEN setweight(to_tsvector('english', title), 'A') || setweight(
        to_tsvector('english', coalesce(short_content, '')),
        'B'
      )
      ELSE setweight(to_tsvector('simple', title), 'A') || setweight(
        to_tsvector('simple', coalesce(short_content, '')),
        'B'
      )
    END
  ) STORED
);

CREATE INDEX IF NOT EXISTS posts_source_id_idx ON posts (source_id);

CREATE INDEX IF NOT EXISTS posts_lang_idx ON posts (lang);

CREATE INDEX IF NOT EXISTS posts_url_idx ON posts (url);

CREATE INDEX IF NOT EXISTS posts_domain_id_idx ON posts (domain_id);

CREATE INDEX IF NOT EXISTS posts_simhash_idx ON posts (simhash);

CREATE INDEX IF NOT EXISTS posts_tags_idx ON posts USING gin (tags);

CREATE INDEX IF NOT EXISTS posts_posted_at_idx ON posts (posted_at DESC);

CREATE INDEX IF NOT EXISTS posts_ts_idx ON posts USING gin (ts);

CREATE TABLE IF NOT EXISTS source_discovered_by_post (
  source_id UUID NOT NULL REFERENCES sources (id),
  post_id UUID NOT NULL REFERENCES posts (id),
  PRIMARY KEY (source_id, post_id)
);

CREATE TABLE IF NOT EXISTS discovery_search_queries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  normalized_entity TEXT NOT NULL, -- TODO: Not sure if `normalized_entity` and `action` is needed, but volvo_cars instead of Volvo Cars
  action TEXT NOT NULL, -- eg buyback, selling/buying
  query TEXT NOT NULL, -- `normalized_entity` and `action` maybe enough to be in only `query`, above can be useful for analytics
  searched_at TIMESTAMPTZ,
  result JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (query)
);

CREATE TABLE IF NOT EXISTS discovery_search_query_posts (
  query_id UUID NOT NULL REFERENCES discovery_search_queries (id) ON DELETE CASCADE,
  post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  PRIMARY KEY (query_id, post_id)
);

CREATE INDEX IF NOT EXISTS discovery_search_query_posts_post_idx ON discovery_search_query_posts (post_id);

CREATE TABLE IF NOT EXISTS discovery_posts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  url TEXT UNIQUE NOT NULL,
  from_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  discovery_search_query_id UUID REFERENCES discovery_search_queries (id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | rejected | duplicate | failed
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  checked_at TIMESTAMPTZ
);

CREATE TABLE discovery_related_posts (
  from_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  to_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  relation TEXT NOT NULL, -- ex: 'discovered', 'same_event', 'follows_up', 'translation_of'
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (from_post_id, to_post_id, relation),
  CHECK (from_post_id <> to_post_id)
);

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The cookie holds the token, only its sha256 is stored.
CREATE TABLE IF NOT EXISTS sessions (
  token_hash BYTEA PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per outbound request, written by the slog handler in internal/lib/logs.
CREATE TABLE IF NOT EXISTS logs (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  type TEXT NOT NULL, -- deepseek | jev | brave | rss | page | trim | ingest | robots | homepage | wordpress
  level TEXT NOT NULL,
  request JSONB NOT NULL,
  response JSONB,
  status INT,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  error TEXT,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS logs_type_created_at_idx ON logs (type, created_at);

CREATE INDEX IF NOT EXISTS logs_metadata_idx ON logs USING gin (metadata jsonb_path_ops);
