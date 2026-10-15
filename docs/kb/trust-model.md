# Trust model

**Status:** designed
**Related:** [classifier-gate](classifier-gate.md), [tag-lifecycle](tag-lifecycle.md), [source-lifecycle](source-lifecycle.md)

## Summary

Trust belongs to sources. A source with `status = 'active'` or `'dormant'` is trusted, a `proposed` one is not, and posts inherit that. Discovered sources start proposed and earn trust through the [source lifecycle](source-lifecycle.md).

## Current state

No tiers exist. Every post comes from a manual source, and every post can create tags.

## Design

| | Trusted | Untrusted |
|---|---|---|
| Source status | `active`, `dormant` | `proposed` |
| Posts ingested | Always | Only if they pass the [gate](classifier-gate.md) |
| Posts propose new tags | Yes | No |
| Posts seed link extraction and search | Yes | No |
| Quality check | Shadow gate, demoted on drift | Hard gate on every post |

Rules:

- Only trusted posts shape the tag vocabulary. Otherwise untrusted content could widen the gate that admits it.
- Crawl depth has no fixed limit. Each hop needs a promotion, so the crawl grows only as fast as promotions allow.
- Trust is read through `posts.source_id`.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual'; -- manual | discovered
```

Source `status` is defined in [source-lifecycle](source-lifecycle.md).

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Manual source status | active | You picked it. |
| Discovered source status | proposed | Must earn trust. |

## Open questions

- None.
