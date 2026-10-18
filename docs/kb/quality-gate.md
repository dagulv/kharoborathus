# Quality gate

**Status:** done
**Related:** [classifier-gate](classifier-gate.md), [trust-model](trust-model.md), [backfill](backfill.md), [architecture](architecture.md)

## Summary

Every post passes a Jev quality check before any DeepSeek call or feed listing. Trusted sources also publish listings, product pages and other junk, which today get summaries, propose tags and seed links and search.

## Current state

[quality.go](../../internal/service/source/quality.go) checks `pending` posts with `Tagger.Quality` in [gate.go](../../internal/service/robot/gate.go). [summaries.go](../../internal/service/source/summaries.go) summarizes `active` posts through `summary_checked`. Tagging, discovery and link extraction read `active` posts only. `summary_pending` is still in the schema until the follow-up migration.

## Design

**Post status**

`posts.status` is `pending`, `active` or `rejected`. Only `active` posts are listed in `/feeds`.

**Flow**

1. The source pipeline and backfill insert every post as `pending`, with the excerpt as `short_content`. The pipeline no longer summarizes.
2. The quality worker takes up to 20 `pending` posts a minute, newest `posted_at` first. A large backfill then drains behind live posts instead of holding them out of the feed.
3. It asks Jev the `article` question from [gate.go](../../internal/service/robot/gate.go) alone, on title and markdown.
4. A score of at least the threshold sets `active`. A lower score sets `rejected`.
5. While Jev is unavailable the tick stops and posts stay `pending`. Any other error skips the post, which stays `pending` for the next tick. An error never hides a post.
6. With the mocked classifier every pending post becomes `active`, since the mock cannot judge quality.
7. Ingest inserts discovered posts as `active`, since `Tagger.Gate` already asked the same question. It no longer summarizes inline.
8. Four workers run side by side on `active` posts. Each tracks its own checked table:
   - Summary, off peak, through `summary_checked`.
   - Tagging, through `tag_checked`.
   - Discovery, through `discover_checked`.
   - Link extraction, through `link_checked`.
9. A post shows its excerpt until its summary lands.

Rules:

- No worker that calls DeepSeek reads a `pending` or `rejected` post. Summaries, tags, events and links never come from junk.
- Duplicate linking, source revive and source silence still count every post. A source that posts only junk stays active.
- The quality pass rate does not flag sources for now. It could later, like the shadow gate does.
- The shadow gate runs as before, on `active` posts only.
- The tag broadness sample reads `active` posts only.
- Existing posts become `active` without a check. Their summaries are kept.
- `sources.status` and `tags.status` share the column name, so queries joining them must qualify `p.status`.
- `summary_pending` is dropped in a later migration, after the new binary runs. Migrations deploy apart from the app, and the old binary still writes the column.
- Posts the old binary inserts between migration and deploy start `pending` and lack `summary_checked`, so they get checked and may be summarized twice.

## Schema changes

```sql
ALTER TABLE posts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('pending', 'active', 'rejected'));
ALTER TABLE posts ALTER COLUMN status SET DEFAULT 'pending';

CREATE INDEX IF NOT EXISTS posts_status_pending_idx ON posts (created_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS summary_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO summary_checked (post_id) SELECT id FROM posts WHERE NOT summary_pending
ON CONFLICT DO NOTHING;
```

Later migration:

```sql
DROP INDEX IF EXISTS posts_summary_pending_idx;
ALTER TABLE posts DROP COLUMN IF EXISTS summary_pending;
```

## Defaults

| Parameter         | Value             | Why                                  |
| ----------------- | ----------------- | ------------------------------------ |
| Quality threshold | 0.8               | Existing `tagThreshold`.             |
| Quality batch     | 20 posts a minute | Matches the tagging batch.           |
| Summary batch     | 20 posts a minute | Existing `summaryBatch`, off peak.   |
| Existing posts    | `active`          | You chose to skip the check on them. |

## Open questions

- None.
