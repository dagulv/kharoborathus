# Discovered ingest

**Status:** designed
**Related:** [link-extraction](link-extraction.md), [search-discovery](search-discovery.md), [classifier-gate](classifier-gate.md), [source-promotion](source-promotion.md)

## Summary

A new `ingest` service turns queued URLs into posts. It reuses the source pipeline's pieces but gates before summarizing, so rejected URLs cost no LLM call.

## Current state

Nothing yet. Reusable pieces:

- `FetchHTML` in [fetch.go](../../internal/service/source/adapter/fetch.go).
- `Trim.Apply` in [trim.go](../../internal/service/source/trim.go). Domains without a rule fall back to `trimGeneric`, and discovered pages count as samples toward a rule.
- Markdown conversion and `Summarizer` from [sourced.go](../../internal/service/source/sourced.go).
- `Tagger` from [tagger.go](../../internal/service/robot/tagger.go).

## Design

Chosen over a `queue` adapter in the source pipeline. That adapter would summarize before gating and mix trusted with untrusted posts.

1. Read `pending` rows from `link_queue`, respecting the daily cap and per-domain rate.
2. Check `robots.txt` for the domain.
3. Fetch, resolve domain, trim, convert to markdown.
4. Reject on failed trimmer checks, short content or a missing title.
5. Run the [gate](classifier-gate.md). Store scores in `gate_scores`.
6. On reject, set `status = 'rejected'`. Only scores are kept. Raw content waits for the S3 TODO in the README.
7. On accept:
   - Get or create the domain's source. A new one is quarantined, see [source-promotion](source-promotion.md).
   - Record `source_discovered_by_post` for the first post that led there.
   - Summarize and insert the post.
   - Insert `post_links` from the origin post with relation `discovered` and metadata `via` and `query`.
   - Mark `tag_checked`, since the gate already tagged it.

## Schema changes

```sql
CREATE TABLE IF NOT EXISTS link_queue (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  url TEXT UNIQUE NOT NULL,
  via TEXT NOT NULL, -- link | search
  from_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  discover_item_id UUID REFERENCES discover_queue (id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | rejected | failed
  gate_scores JSONB NOT NULL DEFAULT '{}',
  error TEXT,
  attempts INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  checked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS link_queue_pending_idx ON link_queue (created_at)
WHERE
  status = 'pending';
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Daily fetch cap | 200 URLs | Scale slowly. |
| Per-domain rate | 1 request per 10 seconds | Politeness. |
| Max attempts | 3 | Then `failed`. |

## Open questions

- None.
