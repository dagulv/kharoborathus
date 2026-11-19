-- NULL when the source has no language of its own.
ALTER TABLE sources ALTER COLUMN lang DROP NOT NULL, ALTER COLUMN lang DROP DEFAULT;

-- A raw post is claimed before its fetch, so a discovered link has no source and no content yet.
-- status also takes missing: extraction found no content.
ALTER TABLE raw_posts
  ALTER COLUMN source_id DROP NOT NULL,
  ALTER COLUMN lang SET DEFAULT '',
  ALTER COLUMN raw_content DROP NOT NULL,
  ALTER COLUMN content DROP NOT NULL,
  ADD COLUMN IF NOT EXISTS discovery_post_id UUID REFERENCES discovery_posts (id) ON DELETE SET NULL;

-- Last enrich write, older than its raw post's content after a resync.
ALTER TABLE posts ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
UPDATE posts SET updated_at = created_at;
