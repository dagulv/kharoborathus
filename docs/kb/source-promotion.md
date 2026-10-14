# Source promotion

**Status:** designed
**Related:** [trust-model](trust-model.md), [discovered-ingest](discovered-ingest.md)

## Summary

Discovered domains that keep passing the gate are suggested as trusted sources. You approve each one. This covers the README TODO "Automatically add sources".

## Current state

Nothing yet. Sources are only added in admin.

## Design

1. A periodic job computes per discovered source: accepted posts and acceptance rate from `link_queue`.
2. A source meeting both thresholds shows as a suggestion in admin.
3. On approval you add a listing or RSS URL and pick an adapter.
4. The source becomes `origin = 'manual'` and `active = true`.
5. On dismissal it is not suggested again.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN IF NOT EXISTS suggested_at TIMESTAMPTZ;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS dismissed_at TIMESTAMPTZ;
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Min accepted posts | 5 | Enough evidence. |
| Min acceptance rate | 60% | Mostly relevant. |

## Open questions

- Should the LLM find the listing or RSS URL for you?
