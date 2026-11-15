# Post trace

## Intent

When a post is rejected, the user wants to see why without reading code or stdout. The trigger was the Jane Street post `fb8e2bd9`. It was rejected by the jev quality check, but the request and the score were nowhere to be found: jev logs to stdout, and no log row knows which raw post it belongs to.

The admin Posts page becomes the place to follow any URL the pipeline touched. It covers published posts, rejected and duplicate ones, and links that failed before anything was extracted. Selecting one shows its content and a step graph of everything that happened to it. That includes every external call with its exact input and output.

## Goals

- Admin Posts lists every raw post, not only published posts, with a status filter.
- A selected post has two tabs, Content and Logs.
- Logs is a vertical step graph in pipeline order. Every step is an accordion row. Opening a row shows its input and output. For an external call, that means the exact request and response from the `logs` table.
- The step that rejected or failed the post is red, opens on load, and states the reason in its collapsed row, for example `Rejected: score 0.12, needs 0.80`.
- Steps after the stopping point show as not reached and do not open.
- Every external call the pipeline makes for one post is logged against it: the article fetch, the quality check, enrich, and discover. Calls shared by a whole source run are left out. Those are the feed, listing, WordPress archive and link-rule calls.
- Internal steps without an external call store their input and output too, so they open like the rest. These are extract, duplicate check, date window, domain check and source match.
- A URL that fails before content exists still gets a row. The list shows its URL with status `failed` when the fetch failed, or `missing` when extraction found no content. Its graph shows the failed fetch call or the empty extract.

## Non-goals

- Backfilling traces for posts processed before this ships. They show whatever already links to them, mostly the fetch call, and nothing more.
- Editing, retrying or re-running a post from the admin.
- Changing what the pipeline accepts or rejects. Only where it records what it did changes.
- A log retention policy.

## Anti-goals

- A graph that hides why a post stopped. If the stopping step has no stored reason, that is a bug.
- A trace that can show a call from a different post. Calls must be linked by id, never guessed by time or domain.
- Recording that changes pipeline outcomes. A failure to write a step or log row must not reject, fail or drop a post.

## Context

- Pipelines: sourced in `internal/core/service/source/sourced.go`, discovered in `discovered.go`, quality in `quality.go`, enrich in `internal/core/service/enrich`. Discovery search lives in `internal/adapter/search`.
- `internal/lib/logs` writes one `logs` row per outbound request through the `calls` logger. Every `With` attr lands in `logs.metadata`, which has a GIN index.
- Only fetch-side calls go through `calls` today. Several calls use the stdout logger instead, so they never reach the table:
  - the quality check (jev)
  - the extractor's rule generation (deepseek)
  - enrich (jev and deepseek)
  - `retryPending`
- Fetch rows carry `source_id`. The discovered page fetch carries `discovery_post_id`. Brave and the jev result filter carry `discovery_search_query_id`. Nothing carries a raw post id.
- `raw_posts` and `posts` link only by URL. `discovery_posts` and `raw_posts` also link only by URL, and that link breaks on a redirect.
- Today a raw post row is created only after extraction succeeds. Every earlier failure leaves no row:
  - a failed article fetch
  - empty content
  - an insert conflict
  - a discovered link that is too old, blocked, or failed to fetch
- The uncommitted migration on this branch already makes `raw_posts.raw_content`, `raw_posts.content` and `sources.lang` nullable, and `scanRawPost` handles NULL.
- The project edits `internal/migrations/001_init.sql` in place.
- Project preference: the main flow is an event chain where every step stores its input and output in a table.

## Decided

**The raw post is the single record.** Every path creates its `raw_posts` row before the first fetch, so every later call and step can carry its id.
- The page adapter creates the row per link, before fetching the article.
- RSS creates it per feed item, before fetching the article page.
- The discovered path creates it when it picks up a `discovery_posts` row, before fetching.
- The insert's `ON CONFLICT (url)` replaces today's `RawPostExists` pre-check.

**Statuses.** These are added to the existing `pending | accepted | rejected | duplicate | failed`:
- `failed` now also means the fetch failed or the domain is blocked.
- `missing` is new and means extraction found no content.
- `published` is not stored. The list derives it from a `posts` row with the same URL.

**Logs.** Every external call made for a post is logged through `calls`, with `raw_post_id` and `step` in metadata.
- The quality check, extract rule generation and enrich must move from the stdout logger to `calls`.
- Discover's brave and jev filter calls stay keyed by `discovery_search_query_id`. The graph reaches them through `discovery_search_query_posts`, from the post to its queries.
- A discovered post also shows its origin as the first step: the brave query that found it, or the post whose outbound link it was. The fetch and filter calls already logged for that query show inside that step.

**Retries.** A step that runs more than once shows once per attempt, in time order. Cases are `retryPending` re-running the quality check and enrich retries.

**UI.**
- One Posts list with status filter chips: All, Published, Rejected, Duplicate, Missing, Failed, Pending.
- Each row shows title or URL, status badge, domain, lang and time.
- Content and Logs tabs on the selected post.
- The accordion step graph follows the approved mockup in `.superpowers/brainstorm/434295-1794748688/content/post-logs-v2.html`:
  - a green dot means passed, grey means an internal step, red means it stopped here, dashed means not reached
  - input and output sit side by side as formatted JSON
  - the header shows the call type, method, URL, status and duration
- Existing filters and links keep working: source, tag, full-text search and pagination.

**Extract rule generation (Claude's call).** Content, title, author and date rules come from 8 sample pages, in a background goroutine that the pipeline never waits for. Such a call has no single owning post, so it is a domain-level call.
- Log it with the domain id.
- A post's Extract step shows which rule it used, or that none existed yet, and links to the call that made that rule.
- It never appears as one of the post's own calls.

**Re-fetch on conflict (Claude's call).** This keeps today's behavior, where a link with no row is fetched again on every sync.
- A sync that hits an existing row fetches it again only when the post stopped at Fetch or Extract. That means `failed` from fetch, or `missing`.
- New page sources still get their first posts once the domain's content rule exists.
- Enrich-failed posts are told apart from fetch-failed ones internally, by having content or by their last step. The UI shows both as `failed`.
- `ListUnenriched` and `retryPending` never pick up rows without content.

## Step order

These are the steps for each path. Each external call happens inside one step.

Sourced:
1. **Fetch.** The post's own article call, `page` or `ingest`. Shared calls stay out of post traces. Those are the feed, listing, WordPress archive, and link-rule trim and deepseek calls. They stay on the Logs page under `source_id`.
2. **Extract.** Internal. deepseek runs when a domain rule is generated. Ends in `missing` on empty content.
3. **Duplicate check.** Internal: simhash within the window. Ends in `duplicate`.
4. **Quality check.** jev. Ends in `rejected` below the threshold.
5. **Enrich.** jev tags and a deepseek summary. Ends in `failed` after retries.
6. **Discover.** brave searches and the jev result filter, through the post's search queries.

Discovered adds **Found by** before Fetch. It also adds these internal steps after Extract:
- **Domain check:** blocked means `failed`.
- **Date window:** older than 7 days means `rejected`.
- **Source match:** an existing source, or a newly proposed one.

## Left to the builder

- Where internal step input and output live. Options are a new `raw_post_steps` table or another shape. It must keep one ordered record per step attempt, linked to the raw post.
- How much of large inputs to keep for internal steps. Raw HTML can be summarized as a size, as in the mockup. External call bodies stay as `logs` stores them today.
- The admin API shape for the list and the trace, and whether the trace is one call or separate per tab.
- How `published` is computed efficiently. The list query joins `posts` on URL today.
- How a post that redirects is handled. The fetched URL and the final URL may differ, and the raw post keeps one.
- Whether `source_id` becomes nullable for discovered links that have no source yet, or the row waits for source match.
- The shadcn-svelte Tabs component install. Use `pnpm dlx`, never `npx`.
- Tests: pipeline tests for each status above, and a store test for the trace query.
