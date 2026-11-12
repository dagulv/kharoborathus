# Discovered ingest

**Status:** done
**Related:** [search-discovery](search-discovery.md), [classifier-gate](classifier-gate.md), [source-lifecycle](source-lifecycle.md), [post-linking](post-linking.md)

## Summary

Turns `discovery_posts` rows into raw posts with step 1's stages: extract, markdown, simhash, insert, quality check. Accepted raw posts then go through enrich like any other.

## Current state

[discovered.go](../../internal/core/service/source/discovered.go): `Discovered.Run` reads the ids enrich and search send for new rows, and runs a catch-up every minute for rows a full channel dropped. It does not start without Jev, since then every page would be accepted.

## Design

One link at a time. Site fetches have no cap. Only the external APIs are limited.

1. Skip rows that are not `pending` or older than 7 days. Older links are stale news and stay `pending`.
2. A url already in `raw_posts` is `duplicate`.
3. A domain whose sources are all disabled or deleted is `failed`, before any request.
4. Fetch through the shared per-host pacer. Redirects are followed.
5. The final url is stored. Its domain and source decide everything after the fetch.
6. No content is `rejected`. A page dated more than 7 days back is `rejected`. Undated pages pass.
7. A blocked final domain is `failed`.
8. Use the domain's oldest enabled source. Otherwise create a `proposed` one and record the referring post in `source_discovered_by_post`.
9. Insert the raw post. A url conflict is `duplicate`. Otherwise the row is `accepted`.
10. Run the quality check. A failed check leaves the raw post `pending` for step 1's retry.

Rules:

- `accepted` means stored as a raw post. The raw post holds the quality outcome.
- Fetch errors set `failed` without retries. Database errors leave the row `pending`.
- A proposed source is enabled only so it is not blocked. It is neither scraped nor trusted, so its posts never queue more links or searches.
- Language comes from `<html lang>`, else the source's language.
- The catch-up takes up to 20 rows a minute, newest first, and stops at the first error.
- `discovery_related_posts` is not written yet.

## Defaults

| Parameter       | Value    | Why                         |
| --------------- | -------- | --------------------------- |
| Pending window  | 7 days   | Older links are stale news. |
| Stale page      | 7 days   | Same as the pending window. |
| Queue           | 100      | `DiscoveredQueue`.          |

## Open questions

- No `robots.txt` check yet.
