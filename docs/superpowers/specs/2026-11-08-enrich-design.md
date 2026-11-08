# Step 2: Enrich

## Intent

Step 1 leaves fetched articles in `raw_posts` as `accepted`, `rejected`, `duplicate` or `pending`. Nothing turns an accepted raw post into a `posts` row yet, so the feed has nothing to show and discovery has no caller.

Step 2 enriches an accepted raw post and publishes it. One off-peak DeepSeek call returns the summary, tag proposals and events. Jev picks the post's tags. One transaction inserts the post and runs discovery, step 3.

It follows the project's event-driven rule: each step persists its result, then hands the next step an id. Work that cannot run now, because DeepSeek is in peak hours, is picked up later by a background job.

## Goals

- Every accepted raw post becomes exactly one post, with a summary and tags, when DeepSeek is off peak.
- One DeepSeek call per post for summary, tag proposals and events.
- Proposed tags are recorded per post, so promotion can count their support later.
- New search queries reach step 4 after commit.
- A post that keeps failing is retried at most 10 times, behind posts that have not failed.

## Non-goals

- Tag promotion, and with it alias writing. This step only creates proposed tags.
- Retrying step 1's `pending` raw posts. The background job here only covers step 2.
- The feed HTTP routes.
- Logging Jev and DeepSeek requests to the `logs` table. The service still has no `calls` logger.

## Anti-goals

- A failed post must never be retried more than 10 times.
- A proposed tag must never be offered to Jev, and a proposal must never add an active key to a post. Active keys on a post come from Jev only.
- An untrusted post must never create a tag.
- The background job must never call DeepSeek in peak hours.

## Design

### Schema

In `001_init.sql`, rework2 starts fresh:

- Drop `raw_posts.meta_content`.
- Add `raw_posts.retry_count INT NOT NULL DEFAULT 0`.
- `raw_posts.content` becomes `JSONB NOT NULL` and holds what step 1 extracted:

```json
{"content": "<markdown>", "title": "...", "authors": ["..."], "posted_at": "2026-11-08T08:00:00Z"}
```

`posted_at` is omitted when unknown. Step 1's `CreateRawPost` writes it, and its scan reads it back into `RawPost.Content`, `Title`, `Authors` and `PostedAt`. `HasDuplicate` and the simhash are unchanged, since the simhash is computed in memory from the markdown.

### Language

Step 1 stores `lang` as its primary subtag, lowercased: `sv-SE` becomes `sv`, `en-us` becomes `en`. Step 2 keys `tag_terms` and Jev's terms on it, and `tag_terms.matcher` and `posts.ts` only recognise exactly `sv` and `en`. An empty `lang` stays empty. Proposals then add no term.

### Trigger

- **Live path.** Step 1's `saveStatus` sink sends the id of each `accepted` raw post to the step 2 channel with a non-blocking send. A full channel drops the id and logs it. The background job finds it later.
- **Background job.** It ticks every minute through DeepSeek's schedule, so it only runs off peak. Each tick it takes up to 20 raw posts that have no `posts` row with the same `url`, were created more than 5 minutes ago, and are either `accepted`, or `failed` with `retry_count < 10`. Order: `retry_count`, then oldest first, so posts that keep failing move behind fresh ones. The 5-minute gap keeps it off posts the live path is handling. It enriches them one at a time and stops the batch on the first error. An outage then costs one call a minute, not twenty.
- **Step 2 workers.** 5 goroutines read the channel. Each id goes through `Enrich`. Outside the off-peak window a worker leaves the id, and the job picks it up.

Both paths call one function:

```go
Enrich(ctx context.Context, l *slog.Logger, rawPostID string) error
```

### Enrich, for one raw post id

1. Load the raw post. Skip it unless no post has its `url` and it is `accepted`, or `failed` with `retry_count < 10`.
2. Skip it if DeepSeek's schedule is not active.
3. Jev scores every `enabled`, `active` tag against the title and markdown, cut at 60,000 runes. Each question carries the key and its terms in the post's language. 50 tags per request. A score of at least 0.8 adds the key. This is the old `Tagger.Tags`, in `port.Classifier` form. Jev runs before DeepSeek, so a Jev outage never wastes a paid DeepSeek call. Without a Jev key, `main.go` passes a nil classifier, as in step 1, and posts get proposal keys only.
4. One DeepSeek call, temperature 0.2, answer as JSON:
   - `summary`: two or three sentences, at most 300 characters, in the article's language. Only stated facts, plain text, not starting with "The article". The rules of the old `summary.md`.
   - `tags`: at most 5, each `{key, term}`. `key` in English, lowercase, underscores. `term` is the same concept in the article's language and base form. Reuse an existing key when one fits. General concepts only: no names, no sentiment. The rules of the old `tag_generate.md`.
   - `events`: the contract in `discovery.EventsSystemPrompt` and `EventsUserPrompt`.

   The user prompt carries the article's language, the existing tag keys (all statuses, capped at 300), the quality tag keys through `EventsUserPrompt`, and the markdown cut at 20,000 runes. The system prompt is static, so DeepSeek can cache it.
5. Proposals, only for trusted posts, meaning the source is `enabled` with `status = 'active'`:
   - `tagKey(key)`, then map it through `tag_aliases`.
   - A key not in `tags` is inserted with `status = 'proposed'`.
   - A key whose tag is `proposed` gets the term in `tag_terms` under the post's language, and the key goes in `posts.tags`.
   - A key whose tag is `active` or disabled adds nothing. Jev already decided the active ones.
6. One transaction:
   - insert the proposed tags and terms,
   - insert the post with `ON CONFLICT (url) DO NOTHING`,
   - if a row was inserted, call `discovery.Discover(ctx, tx, post, events)`.

   Post fields:

   | Column | Value |
   |---|---|
   | `source_id`, `url`, `lang`, `domain_id`, `simhash` | From the raw post. |
   | `title`, `authors`, `posted_at` | From `raw_posts.content`. |
   | `content` | The markdown. |
   | `short_content` | The summary. If it is empty, the first 280 characters of the markdown as plain text. |
   | `tags` | Jev keys plus proposed keys, deduplicated. |

7. After commit, send each returned query id with `discovery.Enqueue`.

### Errors

| Case | Raw post after |
|---|---|
| Schedule closed, shutdown | Untouched. Not an attempt. |
| Any other error, from Jev, DeepSeek or the database | `status = 'failed'`, `retry_count + 1`. |
| Success | Untouched. The post's `url` now exists in `posts`. |

`failed` on a raw post always means step 2 failed. Step 1's quality errors keep `pending`, so the two never mix. After 10 failures a post stays `failed` for good.

### Feed rule

`posts.tags` mixes active and proposed keys. Anything that shows tags or filters by tag only uses keys whose tag is `enabled` and `active`. Promotion counts a proposed key's support from `posts.tags`.

## Calls made without asking

- The `raw_posts.content` JSON keys: `content`, `title`, `authors`, `posted_at`.
- 5 step 2 workers, a 5-minute gap for the job, a batch of 20, a one-minute tick.
- Proposals add only proposed keys to `posts.tags`, and terms only to proposed tags.
- Proposed tags get the default type, `quality`.
- An empty summary falls back to an excerpt instead of failing.
- A closed schedule and shutdown do not count as attempts.
- Step 1 quality errors stay `pending`, not `failed`.
- Jev runs before DeepSeek, and the job stops its batch on the first error.
- Without Jev, posts get proposal keys only.
- `lang` is normalized in step 1 to its primary subtag.

## Left to the builder

- Package layout. Suggested: `internal/core/service/enrich` for the service, the job and the prompt, and `internal/adapter/postgres/post.go` and `tag.go` for the stores.
- Channel buffer size.
- How the static system prompt is split into sections.
- Tests. At least:
  - the content JSON round trip,
  - each row of the errors table, and the 10-retry cap,
  - proposals for trusted and untrusted posts, and for new, proposed, active and aliased keys,
  - the Jev batch and threshold,
  - an existing post url,
  - query ids enqueued only after commit,
  - the job's selection and its stop on peak hours.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Summary length | 300 characters | Old summary prompt. |
| Excerpt fallback | 280 characters | Old `excerptLen`. |
| Tag proposals per post | 5 | Old tag generator. |
| Existing keys in prompt | 300 | Same cap as events. |
| Article in prompt | 20,000 runes | Old discovery input cap. |
| Jev tag threshold | 0.8 | Old `tagThreshold`. |
| Jev tags per request | 50 | Old `tagsPerRequest`. |
| Jev state | 60,000 runes | Old `maxTagState`. |
| Job batch and tick | 20 per minute | Old worker batch. |
| Job gap | 5 minutes | Clear of the live path. |
| Step 2 workers | 5 | Same as the source pipeline. |
| Max retries | 10 | You picked it. |
