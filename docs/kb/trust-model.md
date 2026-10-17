# Trust model

**Status:** done
**Related:** [classifier-gate](classifier-gate.md), [tag-lifecycle](tag-lifecycle.md), [source-lifecycle](source-lifecycle.md)

## Summary

Trust belongs to sources. An enabled source with `status = 'active'` or `'dormant'` is trusted. A `proposed` or disabled one is not, and posts inherit that. A check constraint keeps deleted sources disabled, so trust checks only read `enabled`. Discovered sources start proposed and earn trust through the [source lifecycle](source-lifecycle.md).

## Current state

`sources.origin` and `sources.status` exist. Only `active` sources are scraped, and manual sync of other or disabled sources returns 409. Link extraction reads trusted posts only. Tag rates and the broadness sample count trusted posts only, and never discovered posts, which all passed the gate. Discovered posts are marked `tag_checked` on insert, so they never propose tags.

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

`origin` is in [schema.sql](../../internal/migrations/schema.sql).

Source `status` is defined in [source-lifecycle](source-lifecycle.md).

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Manual source status | active | You picked it. |
| Discovered source status | proposed | Must earn trust. |

## Open questions

- None.
