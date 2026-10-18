# Classifier gate

**Status:** done
**Related:** [trust-model](trust-model.md), [tag-lifecycle](tag-lifecycle.md), [discovered-ingest](discovered-ingest.md)

## Summary

The Jev tagging call doubles as a relevance gate. Discovered content must pass it. Trusted content runs it in shadow mode to flag sources that drift.

## Current state

While the `shadow_gate` switch is on, the tagging worker stores the shadow gate result in `tag_checked.gate_passed`, see [gate.go](../../internal/service/tagging/gate.go). Daily maintenance sets `sources.flagged_at`, and admin lists flagged sources. The hard gate is `Tagger.Gate` in [gate.go](../../internal/service/robot/gate.go), run by [discovered ingest](discovered-ingest.md). The article question rides in the first Jev batch.

## Design

**Hard gate for discovered content**

1. Run `Tagger` on the trimmed markdown.
2. Add one question to the same Jev batch: does this page report a specific business event, like a news article, press release or filing?
3. Accept if the article question passes and any tag with `gate = true` scores at least the threshold.
4. Discovered content uses the same threshold as tagging. Tune it later from `link_events.gate_scores`.

**Shadow gate for trusted content**

1. The tagging worker runs as today. The steps below run only while the `shadow_gate` switch is on, see [rollout](rollout.md).
2. A trusted post passes if it gets any of its source's `target_tags`. If `target_tags` is empty, any gate tag counts. Target tags are normalized like tag keys and resolved through `tag_aliases`. Targets that name no enabled, active tag are ignored, and a source left with none falls back to gate tags.
3. Store the result. Never reject the post. The result is NULL when the classification failed, or when the source has no target tags and no gate tag exists.
4. A source with a full pass rate window whose pass rate drops below the floor gets `flagged_at`. A source with fewer gate-checked posts is never flagged, so turning the switch on flags nothing early.
5. A flagged discovered source is demoted to `proposed`, see [source lifecycle](source-lifecycle.md). A flagged manual source keeps running.
6. You review flagged sources only, and keep, retarget or disable them. Every review clears `flagged_at` and sets `reviewed_at`. The window only counts posts checked after `reviewed_at`, so a reviewed source needs a fresh full window before it can be flagged again.

## Schema changes

```sql
ALTER TABLE tag_checked ADD COLUMN IF NOT EXISTS gate_passed BOOLEAN;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS flagged_at TIMESTAMPTZ;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
```

## Defaults

| Parameter        | Value         | Why                      |
| ---------------- | ------------- | ------------------------ |
| Tag threshold    | 0.8           | Existing `tagThreshold`. |
| Pass rate window | last 50 posts | Smooths one bad day.     |
| Pass rate floor  | 70%           | Flags clear drift only.  |

## Open questions

- None.
