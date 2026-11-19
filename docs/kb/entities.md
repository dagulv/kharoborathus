# Entities

**Status:** in progress
**Related:** [search-discovery](search-discovery.md), [categories](categories.md), [enrich](enrich.md), [tag-lifecycle](tag-lifecycle.md)

## Summary

An entity is a specific topic posts can be about: a company, an organization, a technology, a product, a strategy or a concept. Entities are more specific than tags. Discovery needs an entity, and prefers the ones that already have posts. You can focus entities so they are always searched. See [the design](../superpowers/specs/2026-11-19-entities-design.md).

## Current state

- [004_entities.sql](../../internal/migrations/004_entities.sql): the tables, the depth query index and the backfill from discovery.
- [entity.go](../../internal/adapter/postgres/entity.go): extraction linking and promotion, the prompt keys, and the public reads `ListPublic` and `GetPublic`.
- [enrich](../../internal/core/service/enrich): extracts up to 8 entities per post and links them in the post's transaction.
- [discover.go](../../internal/adapter/search/discover.go): events resolve to linked entities and only active or focused ones create queries. Focused entities get a depth query.
- [entities/route.go](../../internal/core/service/entities/route.go): the public `GET /entities` and `GET /entities/{key}`.
- [post.go](../../internal/adapter/postgres/post.go): `GET /posts?entity=<key>` through `entityFilter`, and `entities` on every post.
- `mergeTag` in `admin.remote.ts` copies a tag's entity links to the target before the tag is deleted.
- Not built: the admin routes and pages, entity merge, and the depth query when focus is turned on. They land with the admin SPA.

## Design

**Extraction.** The enrich DeepSeek call returns at most 8 entities with `key`, `name`, `term`, `kind` and `tags`. The prompt lists up to 300 existing keys, focused first, then active and proposed by strength.

**Linking.** In the post's transaction, for each extracted entity in order:

1. Drop it unless its `term` is in the article. A term of 3 characters or fewer must match a whole word.
2. Normalize the key like a tag key. Drop an unknown kind.
3. Resolve it by key, then alias, then term in any language.
4. A disabled entity adds nothing. Otherwise link the post and save the term under the post's language.
5. If nothing resolved and the post is trusted, create the entity as `proposed` with its listed tags that are enabled and active.
6. Untrusted posts link to existing entities and never create one.

Entities are locked in key order, so two posts cannot deadlock.

**Promotion.** A proposed entity becomes `active` once 3 linked posts from enabled, active sources on 2 distinct domains exist within 30 days. It is checked when a post links, so the post that tips it can already trigger a search.

**Strength.** The number of posts linked in the last 30 days, from any source. It is computed on read and never stored.

**Discovery.** See [search-discovery](search-discovery.md).

- An event's entity must be one of the post's linked entities.
- A trusted post starts an event search only for an active or focused entity.
- A focused entity gets a weekly depth search, linked to the post that triggered it.
- When searches are held back, focused entities go first, then the strongest.

**Public API.**

- `GET /entities` answers active, enabled entities with `key`, `name`, `kind`, `terms`, `tags`, `strength` and `createdAt`. It is ordered by strength, then key, and never null.
- Filters are `kind`, `tag`, `category`, `limit` and `offset`. The limits are the posts route's.
- `tags` holds enabled, active tags only. The `tag` and `category` filters count those too.
- `GET /entities/{key}` answers one entity. An alias key gives its target. An unknown, proposed or disabled entity gives 404.
- `GET /posts?entity=<key>` keeps posts linked to the entity. Repeating it needs every entity, like `tag`. At most 10 distinct keys. A proposed, disabled or unknown key gives an empty list.
- A post carries `entities`, the keys of its enabled, active linked entities ordered by key.

**Merge.** Merging tag `from` into `to` copies `from`'s entity links to `to` before `from` is deleted. Merging entities is part of the admin and is not built yet.

## Schema changes

```sql
CREATE TABLE entities (key TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'proposed', enabled BOOLEAN NOT NULL DEFAULT true, focus BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE entity_aliases (alias TEXT PRIMARY KEY, key TEXT NOT NULL REFERENCES entities (key) ON DELETE CASCADE, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE entity_terms (key TEXT REFERENCES entities (key) ON DELETE CASCADE, lang TEXT, term TEXT, PRIMARY KEY (key, lang, term));
CREATE TABLE entity_tags (entity TEXT REFERENCES entities (key) ON DELETE CASCADE, tag TEXT REFERENCES tags (key) ON DELETE CASCADE, PRIMARY KEY (entity, tag));
CREATE TABLE post_entities (post_id UUID REFERENCES posts (id) ON DELETE CASCADE, entity TEXT REFERENCES entities (key) ON DELETE CASCADE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (post_id, entity));
```

Depth queries repeat the same text weekly, so the unique index on `discovery_search_queries.query` leaves out `action = 'depth'`. The migration also backfills entities from the entities discovery already recorded.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Entities per post | 8 | A post names more topics than it has themes. |
| Entity keys in prompt | 300 | Same cap as tags. |
| Strength window | 30 days | A month shows a topic is live. |
| Promotion | 3 trusted posts, 2 domains, 30 days | Independent sources, like tag promotion. |
| Depth window | 7 days | Same as the event dedupe. |
| Depth freshness | `pm` | A week between searches, a month of material. |
| Depth searches a day | 50 | About a sixth of the 322 cap, so events keep priority. |
| Name length | 200 characters | Long official names. |
| Entity filters per request | 10 | Each one adds a subquery to the feed query. |

## Open questions

- The admin routes and pages, and entity merge. They need the admin SPA first.
