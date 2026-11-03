# Extraction

**Status:** done
**Related:** [architecture](architecture.md), [source-lifecycle](source-lifecycle.md), [discovered-ingest](discovered-ingest.md)

## Summary

One `Extractor` port reads each field of a post from html: feed page links, content, title, authors and postedAt. Each field has its own LLM-generated CSS rule per domain, so one bad selector cannot spoil the others.

## Current state

[extract.go](../../internal/core/port/extract.go) defines the port. The html adapter in [extract/html](../../internal/adapter/extract/html) holds the rules, sampling, generation and checks. Core calls it from the `Extract` step in [extract.go](../../internal/core/service/source/extract.go), and the page fetcher calls `Links` in [page.go](../../internal/adapter/fetcher/page.go). Core never sees a rule type.

## Design

Reading a field:

1. Read the field's rule from `domains.metadata.extract`.
2. Apply it. Title and authors read the text of the match. postedAt reads `datetime`, then `content`, then the text.
3. Without a rule, with a stored abstain, or when the rule matches nothing, fall back:
   - content: the densest node.
   - title: `h1`, `og:title`, `title` meta.
   - authors: `author`, `article:author` meta.
   - postedAt: `time[datetime]`, `article:published_time`, `datePublished` meta.
4. A missing rule adds the page to the domain's sample pool.

The `Extract` step per article:

1. Resolve the domain.
2. Call `Content`. A page failing the checks keeps the content the fetcher gave.
3. Call `Title`, `Authors` and `PostedAt` only for fields the fetcher left empty.
4. Skip the post when it still has no content.

Generating rules:

1. Article fields share one pool of 8 pages per domain. The model sees 3 and the rule is checked on the other 5.
2. A field is generated only once a call asked for it. Feeds that carry their own authors never trigger an authors rule.
3. When the pool is full and the LLM schedule allows it, each requested field gets its own call and is saved on its own.
4. Links keep their own flow, see [source lifecycle](source-lifecycle.md): one listing first, else 3 listings 30 minutes apart.

Rules:

- Content checks fail a page on paywall text, short or long output, high link density, a repeated title or unterminated text.
- A content rule must keep 95% of the generic extraction's paragraphs on every held-out page and remove something.
- A title, authors or postedAt rule must find a value on 3 of the 5 held-out pages. Every value found must be sane: titles of 5 to 300 characters, at most 10 authors of 2 to 100 characters, and no date more than a day ahead.
- Selectors may only use names seen in the sample pages, and generated class names are refused.
- An abstained title, authors or postedAt rule is stored. It means the site does not show that field, and it is not asked again.
- An abstained or failed content or link rule waits 24 hours before a retry. So does a failed title, authors or postedAt rule.
- An unavailable LLM keeps the samples for the next try.
- Saving one field's rule merges its key, so it never drops another field's rule.
- Sampling state lives in memory. A restart starts the pools over.

## Schema changes

None. Rules live in the existing `domains.metadata` column:

```json
{ "extract": { "links": {}, "content": {}, "title": {}, "authors": {}, "postedAt": {} } }
```

## Defaults

| Parameter            | Value  | Why                                                       |
| -------------------- | ------ | --------------------------------------------------------- |
| Pages per pool       | 8      | 3 shown to the model, 5 held out to check the rule.       |
| Field hits           | 3 of 5 | Some articles lack a byline or a date.                    |
| Retry after failure  | 24h    | Bounds LLM spend on sites that never yield a rule.        |
| Sampled domains      | 200    | Discovered domains are often seen once. Oldest go first.  |
| Listings for links   | 3      | 30 minutes apart, so headlines change between them.       |

## Open questions

- Each new domain can cost up to 4 rule generations instead of 1. Watch DeepSeek spend after rollout.
