# Enrich

**Status:** done
**Related:** [quality-gate](quality-gate.md), [search-discovery](search-discovery.md), [tag-lifecycle](tag-lifecycle.md), [trust-model](trust-model.md)

## Summary

Step 2 of the event chain. Turns each accepted raw post into a post with a summary and tags, and runs discovery, off peak. See [the design](../superpowers/specs/2026-11-08-enrich-design.md).

## Current state

- [enrich.go](../../internal/core/service/enrich/enrich.go): `Enrich` handles one raw post id.
- [run.go](../../internal/core/service/enrich/run.go): 5 workers read accepted ids from step 1, and a catch-up job ticks through DeepSeek's schedule.
- [prompts/enrich.md](../../internal/core/service/enrich/prompts/enrich.md): one DeepSeek prompt for the summary, tag proposals and events.
- [tags.go](../../internal/core/service/enrich/tags.go): Jev scores active tags.
- Stores: [raw_post.go](../../internal/adapter/postgres/raw_post.go), [post.go](../../internal/adapter/postgres/post.go), [tag.go](../../internal/adapter/postgres/tag.go).

## Design

1. Step 1 stores `raw_posts.content` as JSON with `content`, `title`, `authors` and `posted_at`, and `lang` as its primary subtag.
2. Step 1's last stage sends each accepted id to step 2 without blocking. A full queue drops it for the catch-up job.
3. `Enrich` skips a raw post unless no post has its url and it is `accepted`, or `failed` with fewer than 10 retries. Outside the off-peak window it leaves the post alone.
4. Jev scores every enabled, active tag in batches of 50. A score of 0.8 or more tags the post. Without Jev, posts get proposed keys only.
5. One DeepSeek call returns the summary, up to 5 tag proposals and up to 5 events.
6. One transaction:
   - Trusted posts save their proposals: new keys become `proposed` tags, and proposed keys get their term and go on the post.
   - The post is inserted. An empty summary falls back to a 280-character excerpt.
   - `Discover` runs.
7. After commit, new search query ids go to step 4.
8. The catch-up job takes up to 20 raw posts a minute, older than 5 minutes, fewest retries first, and stops at the first error.

Rules:

- Any error, from Jev, DeepSeek or the database, sets the raw post `failed` and adds one to `retry_count`. A closed schedule and shutdown do not count.
- `failed` on a raw post always means enrich failed. Step 1's quality errors stay `pending`. Step 1 rechecks them every minute, up to 20 older than 5 minutes, and stops at the first error.
- `posts.tags` holds active and proposed keys. The feed shows only enabled, active ones. Promotion counts support from proposed ones.

## Schema changes

`raw_posts.content` is `JSONB`, `raw_posts.meta_content` is gone and `raw_posts.retry_count` is new, in [001_init.sql](../../internal/migrations/001_init.sql).

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Summary | 300 characters | Old summary prompt. |
| Excerpt fallback | 280 characters | Old excerpt. |
| Tag proposals | 5 | Old tag generator. |
| Jev tag threshold | 0.8 | Old tagger. |
| Catch-up | 20 a minute, 5-minute gap | Old worker batch. |
| Workers | 5 | Same as the source pipeline. |
| Max retries | 10 | Your pick. |

## Open questions

- Jev and DeepSeek requests still log to stdout, not `logs`.
