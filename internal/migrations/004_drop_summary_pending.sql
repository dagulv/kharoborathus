-- Safe only once no running binary writes summary_pending.
DROP INDEX IF EXISTS posts_summary_pending_idx;
ALTER TABLE posts DROP COLUMN IF EXISTS summary_pending;
