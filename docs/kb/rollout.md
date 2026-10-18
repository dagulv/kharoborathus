# Rollout

**Status:** done
**Related:** [index](index.md)

## Summary

Ships the design in six phases, in build order. Ingest starts once phase 1 runs. Features that need ingested data sit behind admin switches, and you turn each on when its readiness numbers look right.

## Current state

All six phases are done, with the switches table and admin page. Every switch starts off. See [architecture](architecture.md) for the system today.

## Design

| Phase | Ships                                                                                              | Switch              | Admin view                                   | Why this order                                                   |
| ----- | -------------------------------------------------------------------------------------------------- | ------------------- | -------------------------------------------- | ---------------------------------------------------------------- |
| 1     | [Tag lifecycle](tag-lifecycle.md) and the `enabled` rename on sources                              | None                | Tags: proposals, aliases, gate flag, enabled | The gate needs gate tags first.                                  |
| 2     | [Classifier gate](classifier-gate.md) in shadow mode on current sources                            | `shadow_gate`       | Flagged sources                              | Tunes thresholds on trusted posts before any discovered content. |
| 3     | [Post linking](post-linking.md)                                                                    | None                | None                                         | Ingest needs duplicate rejection.                                |
| 4     | [Link extraction](link-extraction.md), [discovered ingest](discovered-ingest.md), proposed sources | `discovered_ingest` | `link_events` status counts                  | First discovered content, from free links only.                  |
| 5     | [Search discovery](search-discovery.md)                                                            | `search_discovery`  | Daily Brave query count against budget       | Paid, so it comes after the free path works.                     |
| 6     | Promotion, dormancy and probe from [source lifecycle](source-lifecycle.md)                         | `source_promotion`  | Source status                                | Needs weeks of `link_events` history.                            |

A phase ships as soon as it is built and does not wait for data. Its switch starts off, so new code can deploy before its data exists.

### Switches

A switch guards a feature that would do harm or cost money if it ran before enough data exists. Features that need no data, or hold back on their own thresholds, have no switch.

- The `switches` table and the admin Switches page ship before phase 2, with no rows.
- One row per switch in `switches`. A missing row means off.
- The phase that builds a feature inserts its row in its migration. Admin lists only switches that do something.
- The worker reads its key at the start of every tick and skips the tick when off. Changes apply without a restart.
- Admin shows readiness next to each toggle. Readiness is never enforced, and nothing but you sets a switch.

| Switch              | While off                                                        | Readiness shown                                                                  | Turn on when                                                     |
| ------------------- | ---------------------------------------------------------------- | -------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| `shadow_gate`       | Tagging runs. No `gate_passed` is stored and nothing is flagged. | Enabled `active` tags with `gate = true`                                         | Several gate tags exist.                                         |
| `discovered_ingest` | Link extraction still fills `link_events`. Nothing is fetched.   | Posts with `gate_passed`, shadow pass rate, flagged sources                      | Shadow pass rates look right and few manual sources are flagged. |
| `search_discovery`  | Event extraction still runs. No Brave calls.                     | `link_events` acceptance rate over the last 7 days                               | Free discovery accepts mostly relevant pages.                    |
| `source_promotion`  | Proposed sources pile up. Dormancy and probe still run.          | Days of `link_events` history, proposed sources meeting the promotion thresholds | Weeks of history exist.                                          |

Migration steps:

- **Phase 1:** existing tags start `enabled` and `active`. The first broadness run sets `gate` and `match_rate`. Existing sentiment tags stay and end up with `gate = false`.
- **Phase 1:** every source is manual, so every post is trusted. Trust checks arrive in phase 4 together with `origin` and source `status`, which existing sources get from column defaults.
- **Every phase:** until the first deploy, schema changes go straight into `schema.sql` and databases are recreated from it.
- **Phases 2, 4, 5 and 6:** `schema.sql` inserts the phase's switch row with `ON CONFLICT DO NOTHING`, so a rerun never resets your choice.

Monitoring:

- Each job logs counters per run through the existing slog handler: processed, accepted, rejected, failed, AI calls.
- Brave usage per day is counted from `discovery_events.result_count` over the last 24 hours of `searched_at`.

## Schema changes

```sql
CREATE TABLE IF NOT EXISTS switches (
  key TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Defaults

| Parameter  | Value | Why                                      |
| ---------- | ----- | ---------------------------------------- |
| New switch | Off   | You turn it on after checking readiness. |

## Open questions

- None.
