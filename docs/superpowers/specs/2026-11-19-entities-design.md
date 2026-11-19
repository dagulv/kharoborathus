# Entities

## Intent

Kharoborathus should go deep, not wide. Knowing a lot about one topic is worth more than knowing a little about many. Today discovery is wide: every acting company in a trusted post becomes a paid search, whether it shows up once or every day.

An entity is a specific topic posts can be about: a company, an organisation, a technology, a product, a strategy or another concept. Entities are more specific than tags. `volvo_cars` sits under the tag `cars`, `solid_state_battery` under `batteries`. A concept can exist both as a tag and an entity. A company is never a tag.

Entities give three things:

- Discovery requires an entity, and prefers entities that already have posts over new ones.
- You can mark entities for focus in the admin. Focused entities are always discovered and get extra searches. Everything also works without you.
- Posts link to entities, so the coming search and analysis can scope to one topic and attach its artifacts to it.

Today the only trace of an entity is `discovery_search_queries.normalized_entity`, a company name as a tag key, written by event extraction.

## Goals

- An `entities` table for any kind of specific topic, not only companies, with terms per language, aliases and links to tags.
- Every post links to the entities it is substantially about, from the enrich DeepSeek call.
- Entities start as proposed and become active when enough independent trusted posts support them, checked when a post links, with no job.
- Event searches only run for active or focused entities. When searches are held back, the strongest entities go first.
- Focused entities get a weekly depth search, not tied to one event.
- The feed filters by entity, and the public API lists entities.
- The admin lists, focuses, switches, edits and merges entities.

## Non-goals

- Search and analysis over entities, and artifacts. They come later and build on this.
- People and places as entities.
- Automatic focus. Only you set it.
- Entity descriptions, logos or other profile data.
- Re-extracting entities from old posts with the LLM. The migration only carries over what discovery already recorded.
- Jev classification of entities.

## Anti-goals

- A proposed entity must never trigger a search unless it is focused.
- An untrusted post must never create an entity.
- A disabled entity must never be linked, searched or shown publicly.
- The system must never set or clear `focus`.
- Jev cost must not grow with the number of entities.
- Depth searches must never push event searches past the daily cap.

## Approaches

- **A separate `entities` table.** Picked. Entities are an open list extracted by DeepSeek and grounded in the text. Jev cost stays the same. Companies fit. Analyses scope by entity key.
- **Tags only, with a kind and a parent tag.** Rejected. Jev asks one question per active tag on every post, so thousands of company tags would multiply its cost. Tag `gate` and `match_rate` assume broad topics, and tags forbid names.
- **Tags for concepts and a light record for companies.** Rejected. Analyses would split across two models depending on the kind of topic.

Builds on [the categories design](2026-11-19-categories-design.md). An entity links to tags, and through them to categories.

## Design

### Schema

New file `internal/migrations/003_entities.sql`.

```sql
CREATE TABLE IF NOT EXISTS entities (
  key TEXT PRIMARY KEY, -- English, as a tag key: volvo_cars, solid_state_battery
  name TEXT NOT NULL, -- display name
  kind TEXT NOT NULL, -- company | organization | technology | product | strategy | concept
  status TEXT NOT NULL DEFAULT 'proposed', -- proposed | active
  enabled BOOLEAN NOT NULL DEFAULT true,
  focus BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An alias is never also a key in entities, so lookups take one hop.
CREATE TABLE IF NOT EXISTS entity_aliases (
  alias TEXT PRIMARY KEY,
  key TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (alias <> key)
);

-- How the entity is written in each language. Matching and search use it.
CREATE TABLE IF NOT EXISTS entity_terms (
  key TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  lang TEXT NOT NULL,
  term TEXT NOT NULL,
  PRIMARY KEY (key, lang, term)
);

CREATE INDEX IF NOT EXISTS entity_terms_lower_idx ON entity_terms (lower(term));

CREATE TABLE IF NOT EXISTS entity_tags (
  entity TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  tag TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE,
  PRIMARY KEY (entity, tag)
);

CREATE INDEX IF NOT EXISTS entity_tags_tag_idx ON entity_tags (tag);

CREATE TABLE IF NOT EXISTS post_entities (
  post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  entity TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (post_id, entity)
);

CREATE INDEX IF NOT EXISTS post_entities_entity_idx ON post_entities (entity, created_at DESC);
```

`discovery_search_queries.normalized_entity` already has the entity key format. It stays, and from now on always names a row in `entities`. No foreign key, so the migration and merges stay simple.

Depth searches repeat the same query text weekly. The unique constraint on `discovery_search_queries.query` becomes a partial unique index that leaves depth queries out:

```sql
ALTER TABLE discovery_search_queries DROP CONSTRAINT IF EXISTS discovery_search_queries_query_key;
CREATE UNIQUE INDEX IF NOT EXISTS discovery_search_queries_query_idx ON discovery_search_queries (query) WHERE action <> 'depth';
```

### Migration backfill

The same migration turns what discovery already recorded into entities:

- Each distinct `normalized_entity` becomes an entity with `kind = 'company'`, `status = 'proposed'` and `name = initcap(replace(key, '_', ' '))`. Today's event prompt only allowed companies and organisations.
- Each post linked through `discovery_search_query_posts` gets a `post_entities` row, with the post's `created_at`.
- Each backfilled entity gets its `name` as an `entity_terms` row in every language of its linked posts, so later posts can resolve to it by term.
- Backfilled entities that already meet the promotion rule become `active`.

A wrong kind or a clumsy name is fixed in the admin.

### Strength

Strength is the number of posts linked to an entity in the last 30 days, from any source. It is computed when read, from `post_entities` and its index. Nothing stores it.

### Extraction in enrich

The enrich DeepSeek call gains `entities`, at most 8:

```json
{"key": "solid_state_battery", "name": "Solid-state battery", "term": "solid state-batteri", "kind": "technology", "tags": ["batteries", "electric_vehicles"]}
```

- `key`: English, lowercase, underscores. Reuse a listed key when it is the same entity.
- `name`: the display name in English, or the official name for a company or organisation.
- `term`: as the article writes it, in base form. Used for grounding and stored as a term in the post's language.
- `kind`: one of the six kinds.
- `tags`: existing tag keys the entity belongs under.

Prompt rules:

- Only entities the article is substantially about, never ones mentioned in passing.
- Specific, more specific than a tag. Not generic words like company, market or price.
- No people, no places.
- The most important first.

The user prompt lists up to 300 existing enabled entity keys: focused ones first, then active ones by strength, then proposed ones by strength. The system prompt stays static.

Events keep their shape. `entity` is still written as in the article, so grounding in `groundKeywords` is unchanged. The events prompt now says the entity should be one of the listed entities, and any kind can be an event's entity, not only companies.

### Resolution and linking

In the enrich transaction, before `Discover`, for each extracted entity in order:

1. Drop it unless `term` appears in the article, compared in lowercase. This is the same grounding events use.
2. Normalize `key` with `TagKey`. Drop an unknown `kind`.
3. Resolve to an existing entity, first match wins:
   - `key` is an entity key,
   - `key` is an alias,
   - `term` matches an existing `entity_terms` row in any language, compared in lowercase.
4. If it resolved:
   - a disabled entity adds nothing,
   - otherwise link the post, and add `term` under the post's language if new.
5. If not, and the post is trusted:
   - insert the entity as `proposed` with its `name`, `kind` and `term`,
   - insert `entity_tags` for the listed tags that exist, are enabled and are active,
   - link the post.
6. Untrusted posts never create entities.

Entities are locked in key order, like tags and events, so concurrent posts cannot deadlock.

After linking, a `proposed` entity is promoted when its trusted support reaches the rule: at least 3 linked posts from enabled, active sources, on at least 2 distinct domains, within 30 days. Checked in the same transaction, so the post that tips it can already trigger a search.

`Discover` receives the resolved entities: key, status, `focus`, and whether the entity was linked by this post.

### Discovery

Step 3, `Discover`, changes for events:

1. An event's entity text resolves the same way as an extracted entity: `TagKey` of the text as a key, then as an alias, then as a term. It must land on one of this post's linked entities. Otherwise it is dropped.
2. `normalized_entity` is that entity's key.
3. Dedupe within 7 days and linking `same_event` are unchanged.
4. A trusted post creates a new query only when the entity is `active` or `focus`. A proposed entity's event is dropped, unless a recent query exists to link to.

Step 4, `RunSearch`, keeps its limits. Only order changes, which matters when the limits hold queries back for the catch-up. The catch-up order becomes:

1. unfiltered paid results,
2. event queries, focused entities first, then by strength,
3. depth queries,
4. newest within each.

Depth queries come last, so they only use budget that waiting event queries leave.

### Depth search

A depth search finds recent material on a focused entity, not one event. It is a query with `action = 'depth'`.

Triggers, both event-driven:

- You turn `focus` on in the admin.
- A trusted post links to a focused entity.

Either one creates a depth query unless one for that entity exists within 7 days. The same lock and window as events apply. The new id goes to `Enqueue` after commit.

- The depth query is linked in `discovery_search_query_posts` to one post: the triggering post, or, when you turn focus on, the entity's newest linked post. That post gives the search language and `from_post_id` for kept URLs, as for events.
- A focused entity with no linked post gets no depth query when you turn focus on. Its first trusted post creates one.
- Later posts in the window are not linked to the depth query. That link stays the `same_event` record for events only.

The query text is the entity's term in the linked post's language, or its `name` when it has no term in that language. The search uses `freshness = pm` instead of `pw`, in that language.

The Jev result filter asks a depth result its own question: does it substantially cover this entity? A result about the entity in passing, a listing, a stock quote or a profile page fails.

At most 50 depth searches run per day, inside the existing daily cap of 322. A held-back depth query waits for the catch-up like any other.

Kept URLs go to discovered ingest as today. The posts that come back link to the entity through extraction, which raises its strength.

### Tag and entity merges

- **Merging tag `from` into `to`** also moves `from`'s `entity_tags` rows to `to`, the same way category rows move.
- **Merging entity `from` into `to`**, in one transaction:
  - move `post_entities`, `entity_terms` and `entity_tags` to `to`, skipping duplicates,
  - repoint aliases to `to` and add `from` as an alias,
  - rewrite `discovery_search_queries.normalized_entity`,
  - set `to.focus` if either was focused,
  - delete `from`.

### Public API

- `GET /posts?entity=<key>`: posts linked to every given entity, the AND rule of `tag`. A disabled or unknown entity gives an empty list.
- `GET /entities`: active, enabled entities with key, name, kind, terms, public tags and strength.
  - Ordered by strength, then key.
  - Filters: `kind`, `tag`, `category`, `limit`, `offset`, using the posts route's limits.
- `GET /entities/{key}`: one entity, the same fields, or 404. An alias key gives the target.
- A public post gains `entities`: keys of its enabled, active linked entities.

### Admin

Built on the admin SPA, after it ships, like the category admin.

| Method and path | Body or query | Answer |
|---|---|---|
| `GET /admin/entities` | `kind`, `status`, `focus`, `search`, `page` | `{entities, total, page, pageSize}`, 50 per page, ordered by focus, then strength |
| `GET /admin/entities/{key}` | none | Entity with terms, aliases, tags, strength, trusted support and its last 20 queries |
| `PATCH /admin/entities/{key}` | any of `{enabled, focus, kind, name}` | 204, 400 or 404. Turning focus on can create a depth query. |
| `PUT /admin/entities/{key}/tags` | `{tags: [key]}` | 204, 400 for an unknown tag, 404 |
| `POST /admin/entities/merge` | `{from, to}` | 204, 400 for the same key, 404 for an unknown target |

- `GET /admin/posts` accepts `entity`.
- The SPA gets an Entities list page with filters and switches, and an entity page with edits, merge and links to posts and queries.
- `search` matches key, name and terms.
- `name` is at most 200 characters.

## Calls made without asking

- Six kinds: company, organization, technology, product, strategy, concept.
- No people or places.
- Entity keys are English, like tag keys.
- Strength is linked posts in 30 days from any source, computed on read.
- Promotion needs 3 trusted posts on 2 distinct domains within 30 days, checked inline.
- Untrusted posts link to existing entities but never create them.
- A disabled entity is never linked.
- At most 8 entities per post. At most 300 entity keys in the prompt, focused first, then strongest.
- Resolution is by key, then alias, then exact term in any language.
- A tag link is set only when the entity is created.
- Depth searches only for focused entities, weekly, `pm` freshness, capped at 50 a day, last in the catch-up order.
- A depth query links to its triggering or newest post, and no later posts.
- The prompt's entity keys include proposed ones after active ones.
- Backfilled entities get their name as a term.
- Events keep their surface-text entity and resolve it like extracted entities.
- The depth query drops out of the unique query constraint.
- Backfill from `normalized_entity` as proposed companies, promoted when they qualify.
- Any kind of entity can be an event's entity.
- No foreign key from `normalized_entity` to `entities`.

## Left to the builder

- Package layout. Suggested:
  - `internal/adapter/postgres/entity.go` for the store,
  - entity resolution in `internal/core/service/enrich`,
  - the depth trigger next to `Discover` in `internal/adapter/search`.
- How `Discover`'s signature carries resolved entities.
- Exact prompt wording.
- A KB page `docs/kb/entities.md`, a row in the index, and updating `docs/kb/search-discovery.md`.
- Tests. At least:
  - the migration and backfill,
  - each resolution path, grounding, disabled and untrusted cases,
  - promotion at the threshold and below it, and on distinct domains,
  - discovery gating for proposed, active and focused entities,
  - catch-up order,
  - depth triggers, the 7-day window, the daily cap, `pm` freshness and the depth filter question,
  - entity merge and tag merge,
  - the feed filter and each public and admin route.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Entities per post | 8 | More than tags, since a post names more topics than it has themes. |
| Entity keys in prompt | 300 | Same cap as tags. |
| Strength window | 30 days | A month shows a topic is live. |
| Promotion | 3 trusted posts, 2 domains, 30 days | Independent sources, like tag promotion. |
| Depth window | 7 days | Same as the event dedupe. |
| Depth freshness | `pm` | A week between searches, a month of material. |
| Depth searches per day | 50 | About a sixth of the 322 cap, so events keep priority. |
| Name length | 200 characters | Long official names. |
