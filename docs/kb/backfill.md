# Backfill

**Status:** done
**Related:** [source-lifecycle](source-lifecycle.md), [architecture](architecture.md)

## Summary

A feed shows only its newest posts. A WordPress site also serves its whole archive through the REST API, so every scraped WordPress source is backfilled once. AI work on the backfilled posts runs in background queues.

## Current state

- [wordpress.go](../../internal/service/source/adapter/wordpress.go) finds the API and reads every post.
- [backfill.go](../../internal/service/source/backfill.go) checks each scraped source once and sends WordPress ones to the `wordpress` pipeline in [sourced.go](../../internal/service/source/sourced.go).
- [summaries.go](../../internal/service/source/summaries.go) summarizes active posts in the background, backfilled or not.
- The admin source check shows the post total, see [route.go](../../internal/service/source/route.go).

## Design

1. Every minute the source worker takes up to 10 sources that are enabled, `active` and not yet backfilled.
2. It fetches the source URL and looks for the REST API root: the `Link` header with `rel="https://api.w.org/"`, sent on pages and feeds, or the same `<link>` in the page head.
3. It probes `wp/v2/posts?per_page=1`, whose `X-WP-Total` header gives the post total.
4. It sets `backfilled_at`, then sends a copy of the source with adapter `wordpress` and the posts endpoint as URL.
5. The `wordpress` adapter pages through the API, 20 posts per request, newest first, until `X-WP-TotalPages`. Requests to one host go at least 5 seconds apart, so 420 posts take about 2 minutes. `_embed=author,wp:term` gives author and category names.
6. The pipeline skips posts already stored, like any other source, and inserts the rest as pending for the [quality gate](quality-gate.md).
7. `RunSummaries` summarizes 20 active posts a minute while DeepSeek is off peak. Until then a post shows its excerpt.

Rules:

- Backfill is automatic. A new source, an existing one and a promoted one are all checked once.
- `backfilled_at` is set before the backfill runs. A failed check or an interrupted backfill is not retried. Clearing `backfilled_at` runs it again, and stored posts are skipped.
- The API gives no language, so posts take the `<html lang>` of the page or the `<language>` of the feed the API was found on.
- Without pretty permalinks the API root is `/?rest_route=/`, and the route goes in that query value.
- A closed API, which answers 401 or 404, means no backfill.
- Tagging, event extraction, search and link extraction already work through background queues with their own batch sizes and budgets. Backfilled posts join them by `created_at`, so a large backfill delays newer posts in those queues.
- API requests log as type `wordpress`.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN backfilled_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN summary_pending BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX posts_summary_pending_idx ON posts (created_at) WHERE summary_pending;
```

These are in `001_init.sql`. `summary_pending` is replaced by `summary_checked`, see [quality gate](quality-gate.md).

## Defaults

| Parameter                      | Value | Why                                                                           |
| ------------------------------ | ----- | ----------------------------------------------------------------------------- |
| Sources checked per minute     | 10    | A first run on many existing sources stays gentle.                            |
| Posts per API request          | 20    | Full content fits well under the 5 MB body cap.                               |
| Summaries per minute           | 20    | Same batch as tagging and discovery.                                          |
| Gap between requests to a host | 5 s   | The site serves full posts per request, which is heavy work for a small host. |

## Open questions

- Other platforms with an archive API, like Ghost or Substack.
