# Knowledge base

Design notes for ingest and tagging. New pages start from [_template.md](_template.md).

## Goal

Grow beyond hand-picked sources without losing quality. Scale slowly and prefer quality over volume. Tags decide what is relevant.

## Pages

| Page                                      | Status | Description                                                                                            |
| ----------------------------------------- | ------ | ------------------------------------------------------------------------------------------------------ |
| [Architecture](architecture.md)           | done   | Current workers, source pipeline, AI providers and unused schema.                                      |
| [Trust model](trust-model.md)             | done   | Trusted versus untrusted sources and what their posts may do.                                          |
| [Tag lifecycle](tag-lifecycle.md)         | done   | Proposal, frequency promotion, aliases, gate eligibility, backfill and dormancy.                       |
| [Classifier gate](classifier-gate.md)     | done   | Jev tagging as a hard gate for discovered content and a shadow gate for trusted sources.               |
| [Link extraction](link-extraction.md)     | done   | Go job queueing outbound links from trusted posts.                                                     |
| [Search discovery](search-discovery.md)   | done   | Brave Web Search for events whose action is a gate tag, across news, press releases and other sources. |
| [Discovered ingest](discovered-ingest.md) | done   | `ingest` service turning queued URLs into gated posts.                                                 |
| [Source lifecycle](source-lifecycle.md)   | done   | Sources move through proposed, active and dormant, with automatic promotion.                           |
| [Post linking](post-linking.md)           | done   | Every relation between posts: discovered, duplicate_of and same_event.                                 |
| [Rollout](rollout.md)                     | done   | Six phases in build order, admin switches for data-dependent features, migration steps and monitoring. |
| [Infra](infra.md)                         | done   | One UpCloud VPS with OpenTofu, cloud-init and Haloy.                                                   |

## Flow

1. Trusted sources produce posts.
2. Trusted posts propose tags and seed link extraction and search.
3. Found URLs enter `link_events`.
4. `ingest` fetches, trims and gates them.
5. Accepted posts join the feed and get linked to duplicates and same-event posts. Their domains become proposed sources.
6. Proposed sources that keep passing the gate are promoted to active and join step 1.
