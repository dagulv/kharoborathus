# Tag lifecycle

**Status:** done
**Related:** [trust-model](trust-model.md), [classifier-gate](classifier-gate.md)

## Summary

New tags are proposed by the LLM, but only go live after enough independent trusted posts agree. Duplicates become aliases, broad tags cannot gate, and unused tags go dormant.

## Current state

- `tags (key PK)` holds only the key.
- `tag_terms (key, lang, term, matcher)` holds one term per language. `matcher` is a stemmed `phraseto_tsquery` used by cross-language search in [feed/store.go](../../internal/service/feed/store.go) and admin `admin.remote.ts`.
- `posts.tags TEXT[]` holds keys with no foreign key.
- [tagged.go](../../internal/service/tagging/tagged.go) runs `TagGenerator` on every post, and `AddTags` makes its output live at once.
- [tagger.go](../../internal/service/robot/tagger.go) asks Jev one question per tag, 50 per request, so cost grows with vocabulary size.

## Design

States: `proposed`, `active`, `dormant`. A merged synonym becomes an `alias`.

`enabled` is your manual switch, same as on sources. The classifier only uses tags with `enabled = true` and `status = 'active'`. Generator output for a disabled key is dropped, so a disabled tag is never re-proposed or reactivated. The lifecycle never changes `enabled`.

1. **Propose.**
   - `TagGenerator` runs only on posts whose source is trusted when they are tagged. Untrusted posts are still classified.
   - Its prompt asks for topic tags only, no sentiment tags like `bullish`. Those can never gate and only add Jev cost.
   - Output is remapped through `tag_aliases`, then written to `tag_proposals`.
   - Proposals stay out of `tags`, because search reads `tag_terms` and would surface them.
2. **Promote.** A periodic job promotes a key when all checks pass:
   - Support: enough posts from enough distinct domains inside the window. Distinct domains stop one site's house jargon. Only posts whose source is still trusted count, so disabling a source withdraws its support.
   - Not a duplicate: the term is tested with `to_tsvector(cfg, term) @@ matcher` against active terms in the same language. Only a hit goes to a Jev merge check, one question per hit in one request. The highest score at or above the tag threshold makes the candidate an alias of that tag.
   - Consistent: Jev scores the candidate on its supporting posts. Most must reach the tag threshold.
   - Broadness: Jev scores the candidate on recent posts. The same pass is the backfill in step 5, so one set of Jev calls gives both. The match rate sets `gate`.
   - A daily job recomputes `match_rate` and `gate` for active tags from `posts.tags`, with no Jev calls, since every post is already classified against every active tag.
3. **Insert.** Promotion reuses `AddTags` to write `tags` and `tag_terms`.
4. **Alias.**
   - The alias term is added as another `tag_terms` row under the canonical key. Search, `matcher` and the Tagger question pick it up for free.
   - `tag_aliases` remaps future generator output.
   - Proposals for the alias key are deleted. Proposed keys were never on posts, so no posts need rewriting.
5. **Backfill.** `tag_checked` is one-time, so promotion classifies only the new tag against recent posts and appends matches.
6. **Dormancy.**
   - `ListTags` returns only enabled `active` tags, so dormant and disabled tags cost nothing.
   - `CompleteTagging` updates `last_matched_at`.
   - A tag with no match inside the dormancy window goes dormant. A new proposal reactivates it and resets `last_matched_at`, so it is not put back to sleep the next day.
7. **Manual merge.** Admin can merge one active tag into another. That is the case where posts are rewritten with `array_replace`, along with `sources.target_tags`.

## Schema changes

```sql
ALTER TABLE tags ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'; -- active | dormant
ALTER TABLE tags ADD COLUMN IF NOT EXISTS gate BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS match_rate REAL;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS last_matched_at TIMESTAMPTZ;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS tag_proposals (
  key TEXT NOT NULL,
  lang TEXT NOT NULL,
  term TEXT NOT NULL,
  post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (key, post_id)
);

CREATE TABLE IF NOT EXISTS tag_aliases (
  alias TEXT PRIMARY KEY,
  key TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE
);
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Support posts | 3 | Filters one-off inventions. |
| Distinct domains | 2 | Filters single-site jargon. |
| Support window | 30 days | Old proposals stop counting. |
| Gate match rate | below 20% | Broader tags cannot filter. |
| Broadness and backfill set | Last 7 days, at most 200 posts | Recent feed gets the tag, and the rate comes for free. |
| Min sample for `gate = true` | 20 posts | A tiny sample gives a meaningless rate. |
| Rate recompute window | 30 days of `posts.tags` | Stable without Jev calls. |
| Dormancy window | 90 days | Drops dead tags from Jev calls. |

All values are untested starting points.

## Open questions

- None. Existing sentiment tags stay and get `gate = false` from the first broadness run, see [rollout](rollout.md).
