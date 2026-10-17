# Discovered ingest

**Status:** done
**Related:** [link-extraction](link-extraction.md), [search-discovery](search-discovery.md), [classifier-gate](classifier-gate.md), [source-lifecycle](source-lifecycle.md), [post-linking](post-linking.md)

## Summary

A new `ingest` service turns queued URLs into posts. It reuses the source pipeline's pieces but gates before summarizing, so rejected URLs cost no LLM call.

## Current state

[ingest.go](../../internal/service/ingest/ingest.go) runs every minute while `discovered_ingest` is on. It reuses `FetchHTML`, `Trim.Apply`, `source.Markdown`, `Summarizer` and the hard gate `Tagger.Gate`. Admin shows `link_queue` status counts on the overview.

## Design

Chosen over a `queue` adapter in the source pipeline. That adapter would summarize before gating and mix trusted with untrusted posts.

1. Read `pending` rows from `link_queue` created inside the pending window, respecting the daily cap and per-domain rate. Runs only while the `discovered_ingest` switch is on, see [rollout](rollout.md). Older rows stay `pending`, so links queued while the switch was off are not fetched stale.
2. Check `robots.txt` for the domain.
3. Fetch, resolve domain and store it in `domain_id`, trim, convert to markdown.
4. Reject on failed trimmer checks, short content or a missing title.
5. Reject with `status = 'duplicate'` when the simhash matches a recent post, see [post linking](post-linking.md).
6. Run the [gate](classifier-gate.md). Store scores in `gate_scores`.
7. On reject, set `status = 'rejected'`. Only scores are kept. Raw content waits for the S3 TODO in the README.
8. On accept:
   - Use the domain's oldest enabled source, whatever its status, or else its oldest source. Otherwise create a `proposed` one, see [source lifecycle](source-lifecycle.md). The post still had to pass the gate either way.
   - Record `source_discovered_by_post` for the first post that led there.
   - Summarize and insert the post.
   - Insert a `discovered` link, see [post linking](post-linking.md).
   - Mark `tag_checked`, since the gate already tagged it.

Rules:

- One link per host per tick, fresh links before retries. The 10-second pacer only separates a host's `robots.txt` fetch from its page fetch.
- The daily cap is a rolling 24 hours of `checked_at`.
- `robots.txt` disallow sets `failed` without retries, since it is no gate decision. A missing `robots.txt` allows everything. 5xx, 429 and network errors retry like a failed fetch. Results are cached per host for 24 hours.
- No enabled, active gate tag means no tick. Otherwise every link would be rejected for good.
- Classifier unavailable stops the tick and leaves the link untouched. Any other gate error charges the link one attempt and stops the tick.
- Private, loopback and link-local addresses are refused and set `failed`.
- A redirect to another host sets `failed`, since `robots.txt` and the pacer covered only the original host. Same-host redirects store the final URL.
- Pages with an extracted date older than the pending window are rejected as `stale`. Undated pages pass.
- Ingest does not run while the classifier is mocked, since the mock passes almost any page.
- Failed trimmer checks and short content are one check: `Trim.Apply` leaves no content, and the link is rejected with `no content`. A missing title rejects with `no title`.
- A URL conflict on insert sets `duplicate` with `url exists`.
- Deleted sources count, so a domain whose source you deleted is not proposed again. When every source of the domain is disabled or deleted, the link fails with `source disabled` and nothing is inserted, so disabling a source blocks its domain. The check runs right after the domain lookup, before trim, the gate or the summary, and again inside the insert transaction.
- `source_discovered_by_post` records the referring post once, when the proposed source is created.
- Language comes from `<html lang>`.
- Accepted posts do not touch `tags.last_matched_at`.

## Schema changes

```sql
CREATE TABLE IF NOT EXISTS link_queue (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  url TEXT UNIQUE NOT NULL,
  via TEXT NOT NULL, -- link | search
  from_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  discover_item_id UUID REFERENCES discover_queue (id) ON DELETE SET NULL,
  domain_id UUID REFERENCES domains (id), -- set on fetch, drives source promotion
  status TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | rejected | duplicate | failed
  gate_scores JSONB NOT NULL DEFAULT '{}',
  error TEXT,
  attempts INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  checked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS link_queue_pending_idx ON link_queue (created_at)
WHERE
  status = 'pending';

CREATE INDEX IF NOT EXISTS link_queue_domain_idx ON link_queue (domain_id, status);
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Daily fetch cap | 200 URLs | Scale slowly. |
| Per-domain rate | 1 request per 10 seconds | Politeness. |
| Max attempts | 3 | Then `failed`. |
| Pending window | 7 days | Older links are stale news. |

## Open questions

- None.
