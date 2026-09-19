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

CREATE TABLE IF NOT EXISTS sources (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  adapter TEXT NOT NULL,
  url TEXT NOT NULL,
  active BOOLEAN NOT NULL DEFAULT false,
  rate INT NOT NULL DEFAULT 15,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  -- metadata JSONB NOT NULL DEFAULT '{}',
);

CREATE INDEX IF NOT EXISTS sources_url_idx ON sources (url);

CREATE INDEX IF NOT EXISTS sources_active_idx ON sources (active);

CREATE TABLE IF NOT EXISTS feeds (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id UUID NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
  url TEXT UNIQUE NOT NULL,
  lang TEXT NOT NULL,
  domain TEXT NOT NULL,
  title TEXT NOT NULL,
  short_content TEXT NOT NULL,
  content TEXT NOT NULL,
  tags TEXT[] NOT NULL DEFAULT '{}',
  posted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- md5_hash TEXT NOT NULL,
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

CREATE INDEX IF NOT EXISTS feed_source_id_idx ON feeds (source_id);

CREATE INDEX IF NOT EXISTS feed_lang_idx ON feeds (lang);

CREATE INDEX IF NOT EXISTS feed_url_idx ON feeds (url);

CREATE INDEX IF NOT EXISTS feed_domain_idx ON feeds (domain);

-- CREATE INDEX IF NOT EXISTS feed_md5_hash_idx ON feeds (md5_hash);
CREATE INDEX IF NOT EXISTS feed_tags_idx ON feeds USING gin (tags);

CREATE INDEX IF NOT EXISTS feed_posted_at_idx ON feeds (posted_at DESC);

CREATE INDEX IF NOT EXISTS feed_ts_idx ON feeds USING gin (ts);
