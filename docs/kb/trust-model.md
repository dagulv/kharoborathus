# Trust model

**Status:** designed
**Related:** [classifier-gate](classifier-gate.md), [tag-lifecycle](tag-lifecycle.md), [source-promotion](source-promotion.md)

## Summary

Trust belongs to sources. A source is either trusted or quarantined, and its posts inherit that. Discovered sources start quarantined and earn trust through [source promotion](source-promotion.md).

## Current state

No tiers exist. Every post comes from a manual source, and every post can create tags.

## Design

| | Trusted | Quarantined |
|---|---|---|
| How a source gets here | Added in admin, or promoted | First accepted post from a new domain, or demoted |
| Scraped on its `rate` | Yes | No, only reached through `link_queue` |
| Posts ingested | Always | Only if they pass the [gate](classifier-gate.md) |
| Posts propose new tags | Yes | No |
| Posts seed link extraction and search | Yes | No |
| Quality check | Shadow gate, demoted on drift | Hard gate on every post |

Rules:

- Only trusted posts shape the tag vocabulary. Otherwise quarantined content could widen the gate that admits it.
- Crawl depth has no fixed limit. Each hop needs a promotion, so the crawl grows only as fast as promotions allow.
- Trust is read through `posts.source_id`.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual'; -- manual | discovered
ALTER TABLE sources ADD COLUMN IF NOT EXISTS trust TEXT NOT NULL DEFAULT 'trusted'; -- trusted | quarantined
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Manual source trust | trusted | You picked it. |
| Discovered source trust | quarantined | Must earn trust. |

## Open questions

- None.
