# Source lifecycle

**Status:** designed
**Related:** [trust-model](trust-model.md), [discovered-ingest](discovered-ingest.md), [classifier-gate](classifier-gate.md), [tag-lifecycle](tag-lifecycle.md)

## Summary

Sources move through `proposed`, `active` and `dormant`, like tags. Discovered domains start proposed, get promoted to active once their posts fit the existing tags, and go dormant when they stop producing. Covers the README TODO "Automatically add sources".

## Current state

Nothing yet. Sources are only added in admin, and the `active` boolean is a manual on and off switch.

Code reading `active` today, to switch to `enabled`:

- [model.go](../../internal/service/source/model.go): `Source.Active`, `Filter.Active`.
- [store.go](../../internal/service/source/store.go): select, filter, insert and update.
- [sourced.go](../../internal/service/source/sourced.go): `activeSources`.
- Admin: `admin.remote.ts`, `types.ts`, `sources/+page.svelte`, `sources/new/+page.svelte`, `+page.svelte` stats.

## Design

States:

| Status | Scraped on `rate` | Posts trusted | Enters by |
|---|---|---|---|
| `proposed` | No | No, gated | First accepted post from a new domain, or demotion |
| `active` | Yes | Yes | Added in admin, promotion, or a dormant probe finding new posts |
| `dormant` | Probe only | Yes | No new posts or repeated fetch failures |

The existing `active` boolean is renamed to `enabled` and stays your manual switch. A source is scraped when `enabled = true` and `status = 'active'`. The lifecycle never changes `enabled`.

1. **Propose.** On the first accepted post from a new domain, [discovered ingest](discovered-ingest.md) creates a source with `origin = 'discovered'`, `status = 'proposed'`, `enabled = true` and `url` set to the domain's base URL. `enabled` must be set explicitly, since the column defaults to false.
2. **Promote.** A daily job checks each proposed source against the `link_queue` rows for its `domain_id`:
   - Enough accepted posts.
   - Spread over enough distinct days, so one news burst cannot promote.
   - A high enough acceptance rate.
3. **Find a listing** on promotion. Fetch the homepage.
   - RSS autodiscovery: a `<link rel="alternate" type="application/rss+xml">` tag gives `adapter = 'rss'` and the feed URL.
   - Otherwise `adapter = 'page'` with the homepage URL. The existing linker generates a link rule after its listing samples.
4. **Cap.** At most a few promotions per week, oldest qualifying first.
5. **Demote.** When the [shadow gate](classifier-gate.md) flags an active discovered source, set `status = 'proposed'`. It must earn promotion again and shows in admin. A flagged manual source stays active for your review.
6. **Dormancy.** An active source goes dormant after no new posts inside the dormancy window, or after repeated consecutive fetch failures.
7. **Probe.** A dormant source is scraped once per probe interval. New posts move it back to `active`.

## Schema changes

```sql
ALTER TABLE sources RENAME COLUMN active TO enabled;
ALTER INDEX sources_active_idx RENAME TO sources_enabled_idx;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'; -- proposed | active | dormant
ALTER TABLE sources ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE sources ADD COLUMN IF NOT EXISTS fetch_failures INT NOT NULL DEFAULT 0;
```

`schema.sql` is an init script for fresh databases and is not rerunnable. Existing databases get changes from numbered files in `internal/migrations`, written to be safe to run twice. The rename is wrapped in a `DO` block that checks `information_schema.columns` for `active` first. `schema.sql` is also updated to say `enabled`.

`fetch_failures` resets on a successful fetch. `origin` is defined in [trust-model](trust-model.md). `flagged_at` is defined in [classifier-gate](classifier-gate.md).

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Min accepted posts | 5 | Enough evidence. |
| Min distinct days | 3 | Not one news burst. |
| Min acceptance rate | 60% | Mostly relevant. |
| Max promotions per week | 5 | Scale slowly. |
| Dormancy window | 30 days | News sources publish often. |
| Max fetch failures | 10 | Site moved or blocks us. |
| Probe interval | 7 days | Cheap check for revival. |

## Open questions

- None.
