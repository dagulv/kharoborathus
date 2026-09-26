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

CREATE TABLE IF NOT EXISTS tags (key TEXT PRIMARY KEY);

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
  ) STORED PRIMARY KEY (key, lang, term)
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
  active BOOLEAN NOT NULL DEFAULT false,
  rate INT NOT NULL DEFAULT 15,
  metadata JSONB NOT NULL DEFAULT '{}',
  target_tags TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS posts_domain_id_idx ON posts (domain_id);

CREATE INDEX IF NOT EXISTS sources_url_idx ON sources (url);

CREATE INDEX IF NOT EXISTS sources_active_idx ON sources (active);

CREATE TABLE IF NOT EXISTS source_discovered_by_post (
  source_id UUID NOT NULL REFERENCES sources (id),
  post_id UUID NOT NULL REFERENCES posts (id),
  PRIMARY KEY (source_id, post_id)
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
  posted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  authors TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  content_hash BYTEA NOT NULL,
  --simhash BIGINT NOT NULL,
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

CREATE INDEX IF NOT EXISTS posts_content_hash_idx ON posts (md5_hash);

CREATE INDEX IF NOT EXISTS posts_tags_idx ON posts USING gin (tags);

CREATE INDEX IF NOT EXISTS posts_posted_at_idx ON posts (posted_at DESC);

CREATE INDEX IF NOT EXISTS posts_ts_idx ON posts USING gin (ts);

CREATE TABLE post_links (
  from_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  to_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  relation TEXT NOT NULL, -- ex: 'discovered', 'duplicate_of', 'same_event', 'follows_up', 'translation_of'
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (from_post_id, to_post_id, relation),
  CHECK (from_post_id <> to_post_id)
);

CREATE INDEX post_links_to_idx ON post_links (to_post_id, relation);

CREATE INDEX post_links_relation_idx ON post_links (relation);
