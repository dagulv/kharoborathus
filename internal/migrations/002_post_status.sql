-- Existing posts are active without a check. New posts wait for the quality worker.
ALTER TABLE posts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('pending', 'active', 'rejected'));

ALTER TABLE posts ALTER COLUMN status SET DEFAULT 'pending';

CREATE INDEX IF NOT EXISTS posts_status_pending_idx ON posts (created_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS summary_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO summary_checked (post_id)
SELECT id FROM posts WHERE NOT summary_pending
ON CONFLICT DO NOTHING;
