# Source promotion

**Status:** designed
**Related:** [trust-model](trust-model.md), [discovered-ingest](discovered-ingest.md), [classifier-gate](classifier-gate.md)

## Summary

Every domain that gets a post accepted becomes a quarantined source. Once it proves its posts fit the existing tags, it is promoted to trusted automatically and scraped like a manual source. Covers the README TODO "Automatically add sources".

## Current state

Nothing yet. Sources are only added in admin.

## Design

1. **Quarantine.** On the first accepted post from a new domain, [discovered ingest](discovered-ingest.md) creates a source with `origin = 'discovered'`, `trust = 'quarantined'`, `active = false` and `url` set to the domain's base URL.
2. **Evaluate.** A daily job checks each quarantined source against the thresholds, using its rows in `link_queue`:
   - Enough accepted posts.
   - Spread over enough distinct days, so one burst cannot promote.
   - A high enough acceptance rate.
3. **Find a listing.** Fetch the homepage.
   - RSS autodiscovery: a `<link rel="alternate" type="application/rss+xml">` tag gives `adapter = 'rss'` and the feed URL.
   - Otherwise `adapter = 'page'` with the homepage URL. The existing linker generates a link rule after its listing samples.
4. **Promote.** Set `trust = 'trusted'`, `active = true` and `promoted_at`. The source is scraped on its `rate` and its posts propose tags and seed discovery.
5. **Cap.** At most a few promotions per week, oldest qualifying first. Keeps growth slow.
6. **Demote.** When the [shadow gate](classifier-gate.md) flags a promoted source, set `trust = 'quarantined'` and `active = false`. It shows in admin for your review.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN IF NOT EXISTS promoted_at TIMESTAMPTZ;
```

`origin` and `trust` are defined in [trust-model](trust-model.md). `flagged_at` is defined in [classifier-gate](classifier-gate.md).

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Min accepted posts | 5 | Enough evidence. |
| Min distinct days | 3 | Not one news burst. |
| Min acceptance rate | 60% | Mostly relevant. |
| Max promotions per week | 5 | Scale slowly. |

## Open questions

- None.
