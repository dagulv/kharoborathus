# Event-driven search discovery

## Intent

Search discovery finds other sites that report the same business events as our posts, and queues their URLs in `discovery_posts`. Today it is two tickers in `internal/adapter/search`: `discovery.Run` polls posts for events and `RunSearch` polls events to search. Both query tables that no longer exist in the reworked `001_init.sql`.

This design makes discovery a link in the event chain. Each step persists its output, then hands the next step an id. No ticker polls for work. A later catch-up job, out of scope here, picks up rows a step left unhandled.

The chain this design plugs into:

1. **Source ingestion.** Fetch, extract, markdown, simhash, quality check, insert into `raw_posts`. An `accepted` id goes to step 2.
2. **Enrich.** Off-peak DeepSeek. One call returns the summary, tag proposals and events. Jev picks post tags. One transaction inserts the post and runs step 3.
3. **Discovery.** No LLM. Outbound links and search queries. Runs inside step 2's transaction. New query ids go to step 4 after commit.
4. **Search.** Brave, then the DeepSeek filter. Kept URLs go into `discovery_posts`.
5. **Discovered ingest.** Pending `discovery_posts` are fetched and fed into step 1.

This design covers the event contract in step 2's call, all of step 3, all of step 4 and the `tag_aliases` table.

## Goals

- Steps 3 and 4 run on events, with no ticker of their own.
- Every step writes its result before the next step starts, so the database shows what is done and what is pending.
- One Brave search per event per 7 days, however many posts report it.
- Posts reporting the same event are grouped through their shared search query.
- A paid Brave search is never lost. Its raw results are stored, so a failed filter can be retried without paying again.
- Synonym actions such as `takeover` resolve to the tag key `acquisition`.

## Non-goals

- Steps 1, 2 and 5 themselves. Step 2 does not exist yet. This design defines what step 2 hands to step 3, not how step 2 summarizes or tags.
- The catch-up job. Rows a step leaves unhandled stay unhandled until it exists.
- The backoff after wasted searches. The old 30 minute to 24 hour pause goes away.
- Writing aliases from discovery. Only tag promotion and manual inserts write `tag_aliases`.
- Legal suffix stripping in entity names, such as `AB` or `ASA`.

## Anti-goals

- A search Brave billed must never be dropped silently when its results came back readable.
- An event must never trigger a Brave search when a query for the same entity and action was created within 7 days.
- Step 3 must never call an LLM.
- Discovery must never create a tag or an alias.

## Design

### Schema

Add to `001_init.sql`. Rework2 rewrites the init schema, so there is no separate migration.

```sql
CREATE TABLE IF NOT EXISTS tag_aliases (
  alias TEXT PRIMARY KEY,
  key TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (alias <> key)
);
```

- `alias` is stored in `tagKey` form: lowercase, whitespace collapsed, spaces and hyphens as underscores.
- Writers keep one rule the table cannot: an alias is never also a key in `tags`. That keeps lookups one hop, with no chains.
- This design adds the table only. No promotion code exists on rework2, so manual inserts are the sole writer until promotion is built.
- Writers are tag promotion and manual inserts. When promotion finds a proposed tag duplicates an active one, one transaction inserts `proposed_key → existing_key`, moves the proposal's `tag_terms` to the existing key and deletes the proposed `tags` row.
- Replace the `-- TODO: Correctly handle tag aliases here.` comment.

Existing tables used, unchanged:

| Table | Use |
|---|---|
| `discovery_search_queries` | One row per event. `normalized_entity`, `action`, `query`, `searched_at`, `result`. |
| `discovery_search_query_posts` | Links every post that reported the event to its query. |
| `discovery_posts` | URLs to ingest. `discovery_search_query_id` is NULL for outbound links. |
| `tags` | Quality tags gate which actions are searched. |

### Step 2 contract

Step 2's single DeepSeek call adds an `events` list to its answer. Each event:

| Field | Example | Rule |
|---|---|---|
| `entity` | `Volvo Cars` | The company that acts, as written in the article. |
| `action` | `acquisition` | An active quality tag key when one fits, else one or two lowercase English words joined by an underscore. |
| `query` | `Volvo Cars köper Northvolt` | Three to six words, in the article's language, containing the entity. |

The events part of the prompt carries over the rules in `prompts/discovery.md`: high precision, only stated events, at most five, entity is the one acting. The prompt receives the active quality tag keys, capped at 300.

Step 2 runs `groundKeywords` on the answer, then calls step 3 inside the transaction that inserts the post:

```go
Discover(ctx, tx, post domain.Post, events []Keyword) (queryIDs []string, err error)
```

After commit, step 2 sends each returned query id to step 4. An error from step 3 rolls back the whole post insert.

What ships now, with no caller until step 2 exists: the `Keyword` event type, `groundKeywords`, the events prompt fragment and `Discover`. No step 2 stub. `Discover` takes a transaction, so it lives where pgx is allowed, not in `core`.

### Step 3: Discovery

Inside step 2's transaction. No LLM, no network.

**Trust.** Per [trust-model](../../kb/trust-model.md), only trusted posts seed links and search. A post is trusted when its source is `enabled` with `status = 'active'`. An untrusted post skips outbound links and never creates a query. It still links to an existing query, as in point 4 under Events, so it joins the event's group. Without this, every discovered page would queue 10 more links with no cap.

**Outbound links.**
1. Collect markdown links from `post.Content`, skipping images.
2. Run `filterLinks` with the post's domain base URL: normalize, strip fragments and `utm_` parameters, drop the own host, blocked hosts and URLs over 2000 characters.
3. Keep the first 10.
4. Insert into `discovery_posts` with `from_post_id` set and `discovery_search_query_id` NULL. Skip URLs already in `posts`. `ON CONFLICT (url) DO NOTHING`.

**Events.** For each grounded event:
1. `action = tagKey(action)`, then map it through `tag_aliases`.
2. Drop the event unless `action` is a key in `tags` with `type = 'quality' AND enabled AND status = 'active'`.
3. `normalized_entity = tagKey(entity)`, for example `volvo_cars`.
4. If a query with the same `normalized_entity` and `action` was created within 7 days, link the post to it in `discovery_search_query_posts`. No new query, no search.
5. Otherwise insert into `discovery_search_queries`. On a `query` conflict, link to the existing row and do not search again.
6. Link the post to the new query and return its id.

A post linked to an existing query is how `same_event` is recorded. Discovery writes no `same_event` row in `discovery_related_posts`.

### Step 4: Search

Triggered by a query id. Runs whenever an id arrives, not only off-peak.

Guards, in order. A failing guard leaves the row untouched with `searched_at` NULL, for the catch-up job:

1. Load the row. Skip it if `searched_at` is set.
2. Pending cap: at most 200 `discovery_posts` that are `pending`, came from a search and were created within 7 days.
3. Daily budget: at most 322 rows with `searched_at` in the last day and `result IS NOT NULL`. That is $50 a month at $5 per 1,000 queries.

Then search Brave with `query`, country `SE` and the language of the query's earliest linked post. That post is also the `from_post_id` and own-host filter for kept URLs.

| Brave outcome | Row after |
|---|---|
| 400 or 422, rejected | `searched_at = now()`, `result` NULL. Never retried. Not counted as paid. |
| 429 or 5xx, unavailable | Untouched. |
| Unreadable response | Untouched. |
| Any other error, such as 401 | Untouched. Logged. |
| Results | `searched_at = now()`, `result = {"results": [...]}` |

Then the filter, only while DeepSeek's off-peak schedule is active:

1. `ResultFilter.Keep` with the entity, action and stored results.
2. Write `result.kept` with the kept ids. In the same transaction, run `filterLinks` on the kept URLs and insert them into `discovery_posts` with the query id. Skip URLs already in `posts`. `ON CONFLICT (url) DO NOTHING`.
3. If the filter fails or the schedule is closed, leave `result` without `kept`.

### `result` column

```json
{
  "results": [
    {"url": "...", "title": "...", "description": "...", "extra_snippets": ["..."], "page_age": "...", "hostname": "..."}
  ],
  "kept": [0, 3]
}
```

`kept` holds indexes into `results`. `SearchResult.URL` is tagged `json:"-"` to keep it out of the filter prompt, so storage needs its own field for the URL.

| `searched_at` | `result` | State | Paid |
|---|---|---|---|
| NULL | NULL | Not searched. | No |
| set | NULL | Rejected by Brave. | No |
| set | `results`, no `kept` | Searched, not filtered. | Yes |
| set | `results` and `kept` | Done. | Yes |

### Runtime

- Step 4 is one goroutine started from `cmd/main.go`. It reads query ids from a buffered channel and handles them one at a time, so the budget and pending checks cannot race.
- Step 2 is the only sender. Until step 2 exists, nothing sends, and step 3 runs only in tests.
- Sends never block. A full channel drops the id with a log line. The row stays `searched_at` NULL for the catch-up job. Step 2 must not stall inside its off-peak window waiting on Brave.
- Without a Brave key the goroutine does not start, and sends drop the same way.
- `discovery.Run`, `RunSearch`, `discoverPosts`, `searchEvents`, `recordWasted`, `nextPause` and `errSearchWasted` go away, along with `Discoverer`, whose prompt work moves to step 2.

## Known tradeoffs

- **Unreadable Brave response.** Brave bills it, but `searched_at` stays NULL, so it is not counted against the budget, and the catch-up job pays for it again. Accepted to keep one rule: `searched_at` is set only when there is something to store.
- **The query dedupe window uses `created_at`.** An event first seen 8 days ago is searched again.

## Calls made without asking

- `tag_aliases` gets `created_at` and `CHECK (alias <> key)` on top of the old table.
- `normalized_entity` is `tagKey(entity)`.
- A `query` conflict links to the existing row instead of searching again.
- Untrusted posts follow the trust model.
- Dropped ids on a full channel or missing Brave key.
- Search language and `from_post_id` come from the earliest linked post.
- Outbound links capped at 10 per post.
- Table only for aliases. No promotion merge code.
- No step 2 stub.

## Left to the builder

- Package layout, matching the rework's core and adapter split. Brave stays in `internal/adapter/search`. The store follows `internal/adapter/postgres`.
- Channel buffer size.
- How step 2's prompt template is split between system and user prompt, as long as the system prompt stays static for DeepSeek caching.
- Tests. At least: `groundKeywords`, alias mapping, the quality gate on actions, the 7-day link to an existing query, the `query` conflict, every Brave outcome in the table above and the filter retry state.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Events per post | 5 | `maxKeywords`, carried over. |
| Tag keys in the prompt | 300 | `maxTagKeys`, carried over. |
| Outbound links per post | 10 | `maxLinksPerPost` in the old ingest. |
| Event dedupe window | 7 days | `dedupeWindow`, carried over. |
| Daily search budget | 322 | $50 a month at $5 per 1,000 queries. |
| Pending search results cap | 200 | One day of ingest at its old daily cap. |
