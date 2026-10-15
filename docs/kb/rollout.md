# Rollout

**Status:** designed
**Related:** [index](index.md)

## Summary

Ships the design in six phases. Each phase produces data the next one depends on, and paid search comes after the free path works.

## Current state

Nothing yet. See [architecture](architecture.md) for the system today.

## Design

| Phase | Ships | Admin view | Why this order |
|---|---|---|---|
| 1 | [Tag lifecycle](tag-lifecycle.md) and the `enabled` rename on sources | Tags: proposals, aliases, gate flag, enabled | The gate needs gate tags first. |
| 2 | [Classifier gate](classifier-gate.md) in shadow mode on current sources | Flagged sources | Tunes thresholds on trusted posts before any discovered content. |
| 3 | [Event linking](event-linking.md) | None | Ingest needs duplicate rejection. |
| 4 | [Link extraction](link-extraction.md), [discovered ingest](discovered-ingest.md), proposed sources | `link_queue` status counts | First discovered content, from free links only. |
| 5 | [Search discovery](search-discovery.md) | Daily Brave query count against budget | Paid, so it comes after the free path works. |
| 6 | Promotion, dormancy and probe from [source lifecycle](source-lifecycle.md) | Source status | Needs weeks of `link_queue` history. |

Migration steps:

- **Phase 1:** existing tags start `enabled` and `active`. The first broadness run sets `gate` and `match_rate`. Existing sentiment tags stay and end up with `gate = false`.
- **Phase 1:** existing sources get `origin = 'manual'` and `status = 'active'` from column defaults.
- **Phase 3:** a one-time job backfills `simhash` and `same_event` links for posts inside the event window.

Monitoring:

- Each job logs counters per run through the existing slog handler: processed, accepted, rejected, failed, AI calls.
- Brave usage per day is counted from `discover_queue.searched_at`.

## Schema changes

None beyond the linked pages.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Min time per phase before the next | 1 week | Enough data to tune. |

## Open questions

- None.
