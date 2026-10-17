# Architecture

**Status:** done
**Related:** [trust-model](trust-model.md), [tag-lifecycle](tag-lifecycle.md), [discovered-ingest](discovered-ingest.md)

## Summary

Kharoborathus collects business and financial news from hand-picked sources, tags it and serves it as a searchable feed. Workers run side by side from [main.go](../../cmd/main.go).

## Current state

| Worker | Code | Job |
|---|---|---|
| HTTP | [feed/route.go](../../internal/service/feed/route.go), [source/route.go](../../internal/service/source/route.go) | Serves `/feeds` and manual source sync. |
| Sources | [sourced.go](../../internal/service/source/sourced.go) | Polls enabled, active sources at their `rate` and dormant ones weekly, and turns articles into posts. |
| Tagging | [tagged.go](../../internal/service/tagging/tagged.go) | Generates new tags, classifies posts against all tags and records the shadow gate result while its switch is on. |
| Discovery | [discovery.go](../../internal/service/discovery/discovery.go) | Extracts events with gate tag keys as actions into `discover_queue` and links `same_event` posts. |
| Search | [search.go](../../internal/service/discovery/search.go) | Searches Brave for gate tag events from trusted posts and queues filtered results while `search_discovery` is on. |
| Links | [extract.go](../../internal/service/ingest/extract.go) | Queues outbound links from trusted posts into `link_queue`. |
| Ingest | [ingest.go](../../internal/service/ingest/ingest.go) | Fetches, gates and inserts queued links while `discovered_ingest` is on. Proposes sources for new domains. |
| Lifecycle | [lifecycle.go](../../internal/service/source/lifecycle.go) | Daily: puts silent sources to sleep and promotes proposed sources while `source_promotion` is on. |

Source pipeline per article:

1. Adapter fetches the listing. `rss` reads the feed. `page` applies an LLM-generated CSS link rule.
2. Skip URLs already in `posts`.
3. Fetch the article.
4. Resolve its domain.
5. Trim with the domain's LLM-generated rule. Without a rule, `trimGeneric` picks the densest node.
6. Convert to markdown.
7. Summarize with the LLM.
8. Insert into `posts` with a simhash and link `duplicate_of`.

AI providers live in [robot/adapter](../../internal/service/robot/adapter):

- DeepSeek is the LLM for rule generation, summaries, tag generation and discovery.
- Jev is the classifier. It answers yes or no questions as scores from 0 to 1.

Brave Web Search is called from [brave.go](../../internal/service/discovery/brave.go).

Unused parts of the schema:

- `post_links` gets `discovered`, `duplicate_of` and `same_event`.

## Design

This page describes the current system. The other pages describe changes.

## Schema changes

None.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Pipeline workers per stage | 5 | `workers` in sourced.go. |
| Tagging and discovery batch | 20 posts per minute | `batchSize` and `tickInterval`. |

## Open questions

- None.
