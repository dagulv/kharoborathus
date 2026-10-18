-- Set when a quality check errors, so a post that keeps failing moves behind fresh ones.
ALTER TABLE posts ADD COLUMN IF NOT EXISTS quality_tried_at TIMESTAMPTZ;
