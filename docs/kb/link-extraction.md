# Link extraction

**Status:** done
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

A Go job pulls outbound links from trusted post bodies into `link_events`. No AI is involved.

## Current state

[extract.go](../../internal/service/ingest/extract.go) scans trusted posts every minute and queues their links. It runs whether or not `discovered_ingest` is on. Parsing and filters live in [links.go](../../internal/service/ingest/links.go). Post content is markdown with absolute links, because the markdown step runs with `converter.WithDomain` in [sourced.go](../../internal/service/source/sourced.go).

## Design

1. Select trusted posts not yet scanned.
2. Parse markdown links from `posts.content`.
3. Normalize: strip `utm_*` parameters and fragments. Drop URLs longer than 2000 bytes, which would break the unique URL indexes.
4. Skip a link when:
   - Its URL is already in `posts` or `link_events`.
   - It points to the post's own domain, which is mostly navigation.
   - Its domain is on the blocklist of social, video and file hosts. The blocklist lives in code until admin needs to edit it.
   - It is not http or https.
5. Insert the rest into `link_events` with `via = 'link'` and `from_post_id`.
6. Mark the post scanned.

Rules:

- Only posts created inside the 7-day pending window are scanned. Older posts would queue stale links with a fresh `created_at`.
- Image links are not links. A linked image `[![alt](img)](href)` is skipped too.
- Own domain compares hosts, lowercased, without `www.` and scheme.

## Schema changes

```sql
CREATE TABLE IF NOT EXISTS link_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`link_events` is defined in [discovered-ingest](discovered-ingest.md).

## Defaults

| Parameter          | Value | Why                                |
| ------------------ | ----- | ---------------------------------- |
| Max links per post | 10    | Link farms at the end of articles. |

## Open questions

- None.
