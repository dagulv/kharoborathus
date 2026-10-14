# Trust model

**Status:** designed
**Related:** [classifier-gate](classifier-gate.md), [tag-lifecycle](tag-lifecycle.md), [source-promotion](source-promotion.md)

## Summary

Content has two tiers. Trusted content comes from sources you add by hand. Discovered content is found by the system and must earn its place.

## Current state

No tiers exist. Every post comes from a manual source, and every post can create tags.

## Design

| | Trusted | Discovered |
|---|---|---|
| Origin | Source added in admin | Links in posts, search results |
| Ingested | Always | Only if it passes the [gate](classifier-gate.md) |
| Can propose new tags | Yes | No |
| Seeds link extraction and search | Yes | No, caps crawl depth at 1 |
| Quality check | Shadow gate plus review of flagged sources | Hard gate |

Rules:

- Only trusted content shapes the tag vocabulary. Otherwise discovered content could widen the gate that admits it.
- A discovered source becomes trusted only through [source promotion](source-promotion.md), with your approval.
- Trust is per source, read through `posts.source_id`.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual'; -- manual | discovered
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Max crawl depth | 1 | Scale slowly. Only trusted posts seed discovery. |

## Open questions

- Should trusted posts seed depth 2 once the gate is tuned?
