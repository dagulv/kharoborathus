# Request logs

**Status:** done
**Related:** [architecture](architecture.md)

## Summary

Every outbound request, to DeepSeek, Jev, Brave or a scraped site, becomes one row in `logs` with its request, response and the ids of what it was for. The rows are written by a slog handler, so callers log through a normal `*slog.Logger`.

## Current state

- [handler.go](../../internal/lib/logs/handler.go) is the slog handler. It follows the [slog handler guide](https://github.com/golang/example/blob/master/slog-handler-guide/README.md) and passes `testing/slogtest`.
- [client.go](../../internal/lib/logs/client.go) wraps an `http.Client` so each request logs one record.
- [main.go](../../cmd/main.go) builds the `calls` logger and hands it to every worker.

## Design

1. `main.go` creates `calls := slog.New(logs.NewHandler(db, l))`. Insert failures go to `l` on stdout.
2. A worker adds the ids it knows: `calls.With("post_id", post.ID)`. Groups nest: `calls.WithGroup("post").With("id", id)` gives `{"post": {"id": ...}}`.
3. Every method that makes a third-party request takes that logger as a parameter, down to the request itself. Nothing is read from the context.
4. The request goes through `logs.Client(client, l, "<type>")`, built per call so it logs to that call's logger.
5. The wrapper logs when the caller closes the response body, or at once when the request fails.

Columns:

- `type` is the record message. One value per client: `deepseek`, `jev`, `brave`, `rss`, `page` (listings and articles), `trim` (link rule checks), `ingest`, `robots`, `homepage` (promotion and adding a page source in admin).
- `request` holds method, url and body. Headers are never stored, since they carry the API keys.
- `response` holds the body as far as the caller read it. JSON bodies are stored as JSON so they can be queried, anything else as a string.
- `level` is `INFO`, `WARN` for a 4xx or 5xx status, `ERROR` when no response came.
- Each DeepSeek retry is its own request and its own row.

Writes and retention, chosen as the simplest option for now:

| Choice | Benefit | Drawback |
|---|---|---|
| Synchronous insert in `Handle` | No goroutine, no queue, no lost rows on shutdown. A row exists as soon as the call returns. | Every request waits for an insert. A slow or down database slows or stalls scraping and AI calls. Pool connections are shared with the workers. |
| One insert per request | Simple. | Many small transactions. A batch insert of many rows would cost less. |
| Full bodies | Every prompt and page can be read back. | Pages up to 5 MB and long prompts make the table grow fast. A listing every 15 minutes is about 10 MB a day per source. |
| No retention | Nothing is lost. | The table and its GIN index grow without bound. |

Ways to change it later:

- Asynchronous writes: `Handle` sends the row to a buffered channel, a goroutine inserts in batches with `CopyFrom`. When the buffer is full a row is dropped and counted on stdout.
- Retention: a daily `DELETE FROM logs WHERE created_at < now() - interval '30 days'`, or monthly partitions dropped whole.
- Smaller rows: cap stored bodies, or skip the response for `page`, `ingest` and `homepage`, which are also kept trimmed in `posts`.

## Schema changes

```sql
CREATE TABLE IF NOT EXISTS logs (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  type TEXT NOT NULL,
  level TEXT NOT NULL,
  request JSONB NOT NULL,
  response JSONB,
  status INT,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  error TEXT,
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS logs_type_created_at_idx ON logs (type, created_at);

CREATE INDEX IF NOT EXISTS logs_metadata_idx ON logs USING gin (metadata jsonb_path_ops);
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Write mode | Synchronous | Simplest. See the table above. |
| Retention | Forever | Simplest. See the table above. |

## Open questions

- Retention period and body size cap, once the table's growth is known.
