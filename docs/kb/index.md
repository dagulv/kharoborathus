# Knowledge base

Design notes for ingest and tagging. New pages start from [_template.md](_template.md).

## Goal

Grow beyond hand-picked sources without losing quality. Scale slowly and prefer quality over volume. Tags decide what is relevant.

## Pages

| Page | Status | Description |
|---|---|---|
| [Architecture](architecture.md) | done | Current workers, source pipeline, AI providers and unused schema. |
| [Trust model](trust-model.md) | designed | Trusted versus discovered content and what each tier may do. |
| [Tag lifecycle](tag-lifecycle.md) | designed | Proposal, frequency promotion, aliases, gate eligibility, backfill and dormancy. |
| [Classifier gate](classifier-gate.md) | designed | Jev tagging as a hard gate for discovered content and a shadow gate for trusted sources. |
| [Link extraction](link-extraction.md) | designed | Go job queueing outbound links from trusted posts. |
| [Search discovery](search-discovery.md) | designed | Brave News search for other coverage of events, query dedupe and LLM filtering of results. |
| [Discovered ingest](discovered-ingest.md) | designed | `ingest` service turning queued URLs into gated posts. |
| [Source promotion](source-promotion.md) | designed | Suggesting reliable discovered domains as trusted sources. |

## Flow

1. Trusted sources produce posts.
2. Trusted posts propose tags and seed link extraction and search.
3. Found URLs enter `link_queue`.
4. `ingest` fetches, trims and gates them.
5. Accepted posts join the feed. Reliable domains get suggested as sources.
