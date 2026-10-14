# Classifier gate

**Status:** designed
**Related:** [trust-model](trust-model.md), [tag-lifecycle](tag-lifecycle.md), [discovered-ingest](discovered-ingest.md)

## Summary

The Jev tagging call doubles as a relevance gate. Discovered content must pass it. Trusted content runs it in shadow mode to flag sources that drift.

## Current state

[tagger.go](../../internal/service/robot/tagger.go) returns keys scoring at least 0.8. Nothing uses the result as a filter. `sources.target_tags` is never read.

## Design

**Hard gate for discovered content**

1. Run `Tagger` on the trimmed markdown.
2. Add one question to the same Jev batch: is this a news article?
3. Accept if the article question passes and any tag with `gate = true` scores at least the threshold.

**Shadow gate for trusted content**

1. The tagging worker runs as today.
2. A trusted post passes if it gets any of its source's `target_tags`. If `target_tags` is empty, any gate tag counts.
3. Store the result. Never reject the post.
4. A source whose pass rate drops below the floor is flagged in admin.
5. You review flagged sources only, and keep, retarget or deactivate them.

## Schema changes

```sql
ALTER TABLE tag_checked ADD COLUMN IF NOT EXISTS gate_passed BOOLEAN;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS flagged_at TIMESTAMPTZ;
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Tag threshold | 0.8 | Existing `tagThreshold`. |
| Pass rate window | last 50 posts | Smooths one bad day. |
| Pass rate floor | 70% | Flags clear drift only. |

## Open questions

- Should discovered content need a stricter threshold than 0.8?
- Should press releases count as articles?
