# Extractor with one rule per field

## Intent

Core asks one `Extractor` port for each field of a post: the article links on a feed page, content, title, authors and postedAt. Each field has its own LLM-generated rule, stored in the domain metadata and owned by its method. The `Trim` pipeline step goes away, and calls to these methods take its place. The pipeline flow otherwise stays as it is today.

Why: today one article rule carries every field, and title, author and published ride along unchecked. Link rules sit behind a separate `Linker`. Core sees three interfaces, two of them generic over rule types, and a fetcher-local `Linker` on top.

## Goals

- One port, `port.Extractor`, with one method per field.
- One rule per field, generated and validated on its own.
- A site with no authors, title or date stores that as a valid answer, so the LLM is not asked again.
- Rule types, sampling, generation and checks stay inside the adapter. Core never sees a rule.

## Non-goals

- No change to skeleton building, selector grounding or the content checks: paywall, length, link density, unterminated text and repeated title.
- No migration of stored rules. Rework2 rewrites `001_init.sql`, so the database starts fresh.
- No wiring of `cmd/main.go` beyond the extractor's constructor. It is mid-rework and references deleted packages.

## Anti-goals

- Saving one field's rule must never overwrite another field's rule.
- A value the fetcher already found must never be replaced by an extracted guess.
- A failed content extraction must never drop content the fetcher gave.

## Design

### Port

```go
type Extractor interface {
	Links(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) ([]string, error)
	Content(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (string, error)
	Title(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (string, error)
	Authors(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) ([]string, error)
	PostedAt(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (time.Time, error)
}
```

`RuleExtractor`, `Linker`, `Fetch`, `Sample`, `Extraction` and `ErrNoRule` leave `port`. Whatever the adapter still needs moves into it.

A zero `time.Time`, an empty title and a nil author list mean nothing was found. That is not an error.

### Storage

- `domain.Metadata.Trim` becomes `Extract map[string]json.RawMessage` with JSON key `extract`.
- Keys: `links`, `content`, `title`, `authors`, `postedAt`.
- The adapter saves through a narrow store interface. The postgres store gets `SetExtractRule(ctx, domainID, key string, rule json.RawMessage) error`. It merges the key into `metadata->'extract'` in one `UPDATE`, so concurrent saves of different fields cannot clobber each other.

### Rules

| Field    | Rule                                                    | Abstain means                    |
| -------- | ------------------------------------------------------- | -------------------------------- |
| links    | `LinkRule`, as today                                    | Failure, retry after 24h         |
| content  | `ContentRule`: include, exclude, strip_after, image_policy | Failure, retry after 24h      |
| title    | `FieldRule`: one selector                               | Stored. The site has no title element |
| authors  | `FieldRule`: one selector, every match is one author    | Stored. The site shows no authors |
| postedAt | `FieldRule`: one selector                               | Stored. The site shows no date   |

Each field has its own prompt files and its own generation call.

### Reading a field

- A stored rule is applied. Field rules select body elements. Title and authors read the text, postedAt reads `datetime`, then `content`, then the text.
- Without a rule, with a stored abstain, or when the rule matches nothing on the page, the field falls back as today:
  - content: the densest node.
  - title: `h1`, `og:title`, `title` meta.
  - authors: `author`, `article:author` meta.
  - postedAt: `time[datetime]`, `article:published_time`, `datePublished` meta.
- `Content` runs the existing checks. A page failing them returns an error that names the failures. The repeated title check uses the page's title, read the same way `Title` reads it.

### Generating

- Article fields share one pool of sample pages per domain, 8 pages as today, 3 shown and 5 held out.
- A method whose rule is missing adds the page to the pool and marks its field as requested for the domain. A page already in the pool is not added twice, so the four methods on one page add it once.
- Only requested fields get rules. RSS posts that carry their own authors never trigger an authors rule.
- The pool collects while a requested field still needs a rule and is not waiting out a failure.
- When the pool is full and the LLM schedule is active, one goroutine generates every requested field still missing, one LLM call each, and saves each on its own. The existing single-generation semaphore stays.
- `ErrUnavailable` puts the samples back. Any other failure marks only that field failed for 24h.
- Links keep today's flow: try a rule from the first listing, else collect 3 listings 30 minutes apart.
- The 200 domain cap and oldest-first eviction stay.

### Validating a field rule

A field rule must find a value on at least 3 of the 5 held-out pages, since some articles lack a byline or date. Every value it finds must be sane:

- title: 5 to 300 characters.
- authors: at most 10 names, each 2 to 100 characters.
- postedAt: parses with the existing layouts, and is not in the future.

Selectors are grounded in the skeleton like today. Of the 3 temperature attempts, the first passing rule wins. If none passes and at least one abstained, the abstain is stored.

### Core pipeline

The `Trim` step becomes `Extract`:

1. Resolve the domain and set `DomainID`.
2. With raw content: call `Content`. An error is logged and the fetched content kept.
3. Call `Title` when the post has none, `Authors` when it has none, and `PostedAt` when it is nil. Errors are logged and skipped.
4. A post still without content is skipped, as today.

### Page fetcher

The page fetcher's local `Linker` interface takes the domain and gets it from a narrow domain store, since it cannot call `Links` without one.

## Calls made by Claude

- Methods take `domain.Domain`, `url` and the raw bytes instead of a `Sample`.
- Title, authors and postedAt share one rule type.
- Field checks need a value on 3 of 5 held-out pages.
- Only fields some call requested get rules.
- A rule that matches nothing on a page falls back to the meta tags, like a stored abstain.
- Field rules select body elements only. Head meta is covered by the fallback.
- The step name `Extract`.

## Left to the builder

- File layout inside `adapter/extract/html`.
- Prompt wording for the three field prompts.
- Test fixtures.
