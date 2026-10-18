# Post linking

**Status:** done
**Related:** [search-discovery](search-discovery.md), [discovered-ingest](discovered-ingest.md), [link-extraction](link-extraction.md)

## Summary

Every relation between two posts is a row in `post_links`. This page owns which relations exist, who writes them and how they are detected. Only the links are stored now. How the feed groups them is decided later.

## Current state

- `duplicate_of` is written by `CreateFeed` in [feed/store.go](../../internal/service/feed/store.go), `same_event` by `CompleteDiscovery` in [discovery/store.go](../../internal/service/discovery/store.go).
- `discovered` is written by `Accept` in [ingest/store.go](../../internal/service/ingest/store.go).
- The feed does not read `post_links` yet.

## Design

### Relations

| Relation       | From       | To                                     | Written by                                | Metadata                                       |
| -------------- | ---------- | -------------------------------------- | ----------------------------------------- | ---------------------------------------------- |
| `discovered`   | Post found | Post whose link or event led to it     | [Discovered ingest](discovered-ingest.md) | `via`: `link` or `search`. `query` for search. |
| `duplicate_of` | Newer post | Earliest post with near-identical text | Simhash check on insert                   | `distance` in bits                             |
| `same_event`   | Newer post | Earliest post of the event             | Event matching after event extraction     | `entity`, `action`                             |

Rules:

- Links point from the newer post to the older one. The older post anchors a group, so `to_post_id` with a relation gives the whole group.
- A pair can have several relations. A search hit is often both `discovered` and `same_event`.
- `follows_up` and `translation_of` from the schema comment stay unused until needed.
- Newer and older mean insert order, `created_at`. The window is measured back from the new post's `created_at`. `posted_at` comes from feeds, can be missing or wrong, and would move existing anchors.

### Same text: `duplicate_of`

Catches wire copies, for example a TT article republished on several sites.

1. Compute a 64-bit simhash of the post's plain text on insert, from word shingles.
2. Compare against posts from the window with `bit_count((simhash # $1)::bit(64))`.
3. Within the distance limit, link the new post to the earliest match.
4. Trusted duplicates are still inserted. Discovered duplicates are rejected in [discovered ingest](discovered-ingest.md) with `status = 'duplicate'`, since they add nothing.

Plain text is the markdown `content` with link targets removed, so `[text](url)` keeps `text`. Words are lowercased runs of letters and digits. Fewer than 3 words form one shingle. Content without words stores no simhash and never links, otherwise every image-only post would match every other. The uint64 hash is stored as its bit pattern in a signed `BIGINT`, so negative values are normal.

### Same event: `same_event`

1. Event extraction runs on every post. Only searches stay limited to trusted posts, see [search-discovery](search-discovery.md).
2. After storing a post's events, find other posts with the same lowercased `entity` and `action` in `discovery_events` within the window.
3. Link the new post to the earliest post of that event.

### Feed

Unchanged for now. Grouped or flat presentation is a later frontend decision.

## Schema changes

Both are in [schema.sql](../../internal/migrations/schema.sql):

```sql
simhash BIGINT, -- on posts
CREATE INDEX IF NOT EXISTS discovery_events_event_idx ON discovery_events (lower(entity), lower(action), searched_at);
```

`discovery_events_event_idx` is defined in [search-discovery](search-discovery.md) and shipped with this phase, since the event lookup reads it first.

## Defaults

| Parameter            | Value        | Why                             |
| -------------------- | ------------ | ------------------------------- |
| Shingle size         | 3 words      | Common for near-duplicate text. |
| Max Hamming distance | 3 of 64 bits | Wire copies with small edits.   |
| Window               | 7 days       | News events are short-lived.    |

## Open questions

- Entity spelling varies, like `Volvo Cars` and `Volvo Personvagnar`. Exact matching misses those. Normalize entities later, similar to tag aliases?
