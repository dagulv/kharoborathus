# Link extraction

**Status:** designed
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

A Go job pulls outbound links from trusted post bodies into `link_queue`. No AI is involved.

## Current state

Nothing yet. Post content is markdown with absolute links, because the markdown step runs with `converter.WithDomain` in [sourced.go](../../internal/service/source/sourced.go).

## Design

1. Select trusted posts not yet scanned.
2. Parse markdown links from `posts.content`.
3. Normalize: strip `utm_*` parameters and fragments.
4. Skip a link when:
   - Its URL is already in `posts` or `link_queue`.
   - It points to the post's own domain, which is mostly navigation.
   - Its domain is on the blocklist of social, video and file hosts.
   - It is not http or https.
5. Insert the rest into `link_queue` with `via = 'link'` and `from_post_id`.
6. Mark the post scanned.

## Schema changes

```sql
CREATE TABLE IF NOT EXISTS link_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`link_queue` is defined in [discovered-ingest](discovered-ingest.md).

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Max links per post | 10 | Link farms at the end of articles. |

## Open questions

- Should the blocklist live in code or in the database?
