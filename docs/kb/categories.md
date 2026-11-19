# Categories

**Status:** done
**Related:** [tag-lifecycle](tag-lifecycle.md), [enrich](enrich.md), [classifier-gate](classifier-gate.md)

## Summary

A category is a group of tags, and a tag can be in several categories. Categories let you browse the feed by field. They also cut Jev's work, since Jev asks about categories first and then only about the tags of matching categories. See [the design](../superpowers/specs/2026-11-19-categories-design.md).

## Current state

- [003_categories.sql](../../internal/migrations/003_categories.sql): the `categories`, `category_terms` and `category_tags` tables, plus the seed of 18 categories and 137 tags.
- [category.go](../../internal/adapter/postgres/category.go): `EnabledCategories` reads enabled categories with their terms and public tag keys.
- [enrich](../../internal/core/service/enrich): two-stage Jev, categories for proposed tags and inline tag promotion.
- [categories/route.go](../../internal/core/service/categories/route.go): the public `GET /categories`.
- [post.go](../../internal/adapter/postgres/post.go): `GET /posts?category=<key>` through `categoryFilter`.
- `mergeTag` in `admin.remote.ts` copies a tag's categories to the target before the tag is deleted.
- The admin routes and pages for categories are not built yet. They land with the admin SPA.

## Design

Categories are a fixed list. The seed gives the first one. The system never creates or enables a category. Only you do, in the admin.

**Two-stage Jev.** Replaces the single tag pass in enrich.

1. Jev gets one question per enabled category. A score of 0.5 or more matches it.
2. The candidate tags are the classifiable tags in a matched category, plus every classifiable tag with no enabled category. Jev asks about them 50 at a time. A score of 0.8 or more adds the key.
3. A classifiable tag is enabled and either active, or proposed and recent. Recent means created, or carried by a post, within 30 days.
4. With no enabled category, stage 1 is skipped and every classifiable tag is a candidate.

A tag is never hidden from Jev by categories. A tag with no categories, or only disabled ones, is always asked.

**Categories for proposed tags.**

1. The enrich DeepSeek call lists the enabled category keys and returns `categories` with each proposed tag.
2. Unknown or disabled keys are dropped.
3. Only the proposal that creates a tag sets its categories. Later proposals never touch them, so clearing categories in the admin sticks.
4. Untrusted posts propose nothing.

**Tag promotion.** After a post is inserted, each proposed key on it becomes active once 5 posts carry it, from enabled and active sources, on 3 distinct domains, created within 30 days. A promoted tag keeps its categories and terms.

**Public API.**

- `GET /categories` answers the enabled categories ordered by key, each with `key`, `enabled`, `terms` by language, `tags` and `createdAt`. It is never null.
- `GET /posts?category=<key>` keeps posts with a public tag in the enabled category. Repeating it needs every category, like `tag`. An unknown or disabled key gives an empty list.

**Merge.** Merging tag `from` into `to` copies `from`'s categories to `to` before `from` is deleted.

A post has no category column. Its categories come from its public tags, meaning enabled and active, so browsing agrees with the tags shown on the post.

## Schema changes

```sql
CREATE TABLE categories (key TEXT PRIMARY KEY, enabled BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE category_terms (key TEXT REFERENCES categories (key) ON DELETE CASCADE, lang TEXT, term TEXT, PRIMARY KEY (key, lang));
CREATE TABLE category_tags (category TEXT REFERENCES categories (key) ON DELETE CASCADE, tag TEXT REFERENCES tags (key) ON DELETE CASCADE, PRIMARY KEY (category, tag));
```

`posts` is unchanged.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Category threshold | 0.5 | Recall over precision, tags decide precision. |
| Tag threshold | 0.8 | Unchanged. |
| Questions per Jev request | 50 | Unchanged. Covers all categories in one request. |
| Category key | 1 to 40 characters | Same as tag keys. |
| Category term | at most 100 characters | Room for multi-word labels. |
| Cache TTL | 1 minute | Same as tags. |
| Proposed tag recency | 30 days | Stale proposals stop costing Jev. |
| Tag promotion | 5 trusted posts, 3 domains, 30 days | Tags are broader than entities, so they need wider support. |

## Open questions

- The admin routes and pages. They need the admin SPA first.
