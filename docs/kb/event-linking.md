# Event linking

**Status:** designed
**Related:** [search-discovery](search-discovery.md), [discovered-ingest](discovered-ingest.md)

## Summary

Links posts that share the same text or cover the same event, using `post_links`. Only the links are stored now. How the feed groups them is decided later.

## Current state

- `posts.content_hash` only catches byte-identical content.
- `--simhash BIGINT` is commented out in [schema.sql](../../internal/migrations/schema.sql).
- `post_links` exists with relations planned in a comment, but has no writers.
- [discovery.go](../../internal/service/discovery/discovery.go) already extracts `entity` and `action` per post.

## Design

**Same text: `duplicate_of`**

Catches wire copies, for example a TT article republished on several sites.

1. Compute a 64-bit simhash of the post's plain text on insert, from word shingles.
2. Compare against posts from the window with `bit_count((simhash # $1)::bit(64))`.
3. Within the distance limit, insert `post_links` from the new post to the earliest match with relation `duplicate_of`.
4. Trusted duplicates are still inserted. Discovered duplicates are rejected in [discovered ingest](discovered-ingest.md) with `status = 'duplicate'`, since they add nothing.

**Same event: `same_event`**

1. Event extraction runs on every post. Only searches stay limited to trusted posts, see [search-discovery](search-discovery.md).
2. After storing a post's events, find other posts with the same lowercased `entity` and `action` in `discover_queue` within the window.
3. Insert `post_links` from the new post to the earliest post of that event with relation `same_event`. The earliest post anchors the event, so `to_post_id` gives the whole group.

**Feed**

Unchanged for now. Grouped or flat presentation is a later frontend decision.

## Schema changes

```sql
ALTER TABLE posts ADD COLUMN IF NOT EXISTS simhash BIGINT;
```

`discover_queue_event_idx` from [search-discovery](search-discovery.md) serves the event lookup.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Shingle size | 3 words | Common for near-duplicate text. |
| Max Hamming distance | 3 of 64 bits | Wire copies with small edits. |
| Window | 7 days | News events are short-lived. |

## Open questions

- Entity spelling varies, like `Volvo Cars` and `Volvo Personvagnar`. Exact matching misses those. Normalize entities later, similar to tag aliases?
