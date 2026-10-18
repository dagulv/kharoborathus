# Source lifecycle

**Status:** done
**Related:** [trust-model](trust-model.md), [discovered-ingest](discovered-ingest.md), [classifier-gate](classifier-gate.md), [tag-lifecycle](tag-lifecycle.md)

## Summary

Sources move through `proposed`, `active` and `dormant`, like tags. Discovered domains start proposed, get promoted to active once their posts fit the existing tags, and go dormant when they stop producing. Covers the README TODO "Automatically add sources".

## Current state

- [Discovered ingest](discovered-ingest.md) creates proposed sources. The source worker scrapes enabled `active` sources at their `rate` and enabled `dormant` sources once per probe interval, see [sourced.go](../../internal/service/source/sourced.go).
- Every listing fetch updates `fetch_failures` through `RecordFetch`. A new post from a dormant source revives it through `Revive`.
- [lifecycle.go](../../internal/service/source/lifecycle.go) runs daily: dormancy always, promotion while `source_promotion` is on. [listing.go](../../internal/service/source/listing.go) finds a feed on the homepage.
- `FlagSources` in [tagging/store.go](../../internal/service/tagging/store.go) demotes trusted discovered sources, active or dormant, in the update that flags them.
- Admin shows promotion readiness on the Switches page and source status on the Sources page.

## Design

States:

| Status     | Scraped on `rate` | Posts trusted | Enters by                                                       |
| ---------- | ----------------- | ------------- | --------------------------------------------------------------- |
| `proposed` | No                | No, gated     | First accepted post from a new domain, or demotion              |
| `active`   | Yes               | Yes           | Added in admin, promotion, or a dormant probe finding new posts |
| `dormant`  | Probe only        | Yes           | No new posts or repeated fetch failures                         |

The existing `active` boolean is renamed to `enabled` and stays your manual switch. A source is scraped when `enabled = true` and `status = 'active'`. The lifecycle never changes `enabled`.

1. **Propose.** On the first accepted post from a new domain, [discovered ingest](discovered-ingest.md) creates a source with `origin = 'discovered'`, `status = 'proposed'`, `enabled = true` and `url` set to the domain's base URL. `enabled` must be set explicitly, since the column defaults to false.
2. **Promote.** A daily job checks each proposed source against the `link_events` rows for its `domain_id`. It runs only while the `source_promotion` switch is on, see [rollout](rollout.md). Dormancy and probe always run.
   - Enough accepted posts.
   - Spread over enough distinct days, so one news burst cannot promote.
   - A high enough acceptance rate.
3. **Find a listing** on promotion. Fetch the homepage. Adding a `page` source in admin runs the same check on its URL through the internal `GET /listing` route.
   - RSS autodiscovery: a `<link rel="alternate" type="application/rss+xml">` tag gives `adapter = 'rss'` and the feed URL.
   - Otherwise `adapter = 'page'` with the homepage URL. The linker first tries a link rule from the first listing fetch, so a new source gets posts at once. If that fails it generates one from three listing samples, 30 minutes apart.
4. **Cap.** At most a few promotions per week, oldest qualifying first.
5. **Demote.** When the [shadow gate](classifier-gate.md) flags an active or dormant discovered source, set `status = 'proposed'`. Dormant counts too, since gate results can land after a source goes dormant and it is still trusted. It must earn promotion again and shows in admin. A flagged manual source stays active for your review.
6. **Dormancy.** An active source goes dormant after no new posts inside the dormancy window, or after repeated consecutive fetch failures.
7. **Probe.** A dormant source is scraped once per probe interval. New posts move it back to `active`.

Rules:

- Promotion evidence counts every `link_events` row of the domain, in either scheme, until the first promotion, so rejections before the proposal lower the rate. A demoted source counts only rows checked since its demotion.
- The acceptance rate is accepted over accepted plus rejected. Distinct days are calendar days of `created_at`, when links were found, so one burst processed over several days cannot promote.
- Promotion fetches the domain base URL, not the stored source URL, which may be a feed after an earlier promotion.
- `promoted_at` records promotions, so the weekly cap ignores probe revivals.
- Only enabled, not deleted proposed sources are promoted.
- Promotion clears `flagged_at` and sets `reviewed_at`, so the shadow gate judges the source on its new posts only.
- A homepage fetch failure skips the source until the next daily run without using the cap.
- Atom autodiscovery counts as RSS. The first matching `<link>` wins. The page adapter uses the final homepage URL when it is on the same domain, ignoring scheme and `www.`, since the linker keeps only links on the listing's host, otherwise the source URL. A homepage that redirects to another domain gives the page adapter on the source URL, and its feed is ignored.
- Homepage, listing and article fetches go through the private address guard, like ingest, since discovered domains choose where they point.
- Dormancy measures from the later of the last post and `status_changed_at`. Only enabled sources go dormant.
- A source goes dormant on a failure once it has 10 consecutive failures and `failing_since` is more than a day old. A short outage of the server's own network then cannot put every source to sleep for a probe interval.
- The probe schedule lives in memory, so a restart probes every dormant source once. A revived source drops the probe pause at the next refresh.
- Only a fresh flag demotes, since demotion happens in the update that flags.

## Schema changes

```sql
ALTER TABLE sources ADD COLUMN status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE sources ADD COLUMN fetch_failures INT NOT NULL DEFAULT 0;
ALTER TABLE sources ADD COLUMN failing_since TIMESTAMPTZ;
ALTER TABLE sources ADD COLUMN promoted_at TIMESTAMPTZ;
```

Until the first deploy these go straight into `schema.sql`. `fetch_failures` resets on a successful fetch. `origin` is defined in [trust-model](trust-model.md). `flagged_at` is defined in [classifier-gate](classifier-gate.md).

## Defaults

| Parameter               | Value   | Why                             |
| ----------------------- | ------- | ------------------------------- |
| Min accepted posts      | 5       | Enough evidence.                |
| Min distinct days       | 3       | Not one news burst.             |
| Min acceptance rate     | 60%     | Mostly relevant.                |
| Max promotions per week | 5       | Scale slowly.                   |
| Dormancy window         | 30 days | News sources publish often.     |
| Max fetch failures      | 10      | Site moved or blocks us.        |
| Min failing time        | 1 day   | A network blip is no dead site. |
| Probe interval          | 7 days  | Cheap check for revival.        |

## Open questions

- None.
