# Admin SPA

## Intent

The admin lives in the SvelteKit web app today. It renders on the server and reads Postgres directly through remote functions. Only sync and the listing check go through the Go app.

The admin does not need SSR. It becomes a client-only SPA, hosted as static files on bunny.net. The Go app serves every admin read and write, and owns login. The SvelteKit web app keeps only the public pages, and gets rebuilt later in a separate project.

The SPA uses shadcn-svelte, themed to look like Linear.

## Goals

- A static SPA in `admin/` with the same pages and actions as today's admin.
- Every admin page reads and writes through `/admin/*` routes on the Go app. The SPA never touches Postgres.
- Login, logout and the session check live in Go. Existing users keep their passwords.
- The SPA runs from `https://admin.kharoborathus.dagulv.me` against `https://api.kharoborathus.dagulv.me`, and in dev from `http://localhost:5173` against `http://localhost:3001`.
- A Linear-like look: compact, neutral, sidebar navigation, light and dark.

## Non-goals

- New admin features: command palette, keyboard navigation, side panels, inline editing. They come later.
- Rebuilding the public web app. It only loses its admin parts.
- Creating the bunny.net zones and the `admin.` DNS record. The user sets those up by hand. CI only uploads and purges.
- Keeping the admin up through the switch. It is down from the API deploy until the SPA is live, and users log in again.
- Rate limiting login.

## Anti-goals

- The session token must never be readable by JavaScript. No localStorage, no bearer header from the SPA.
- No admin route may answer without a valid session, apart from login and CORS preflight.
- No admin route may accept a credentialed request from an origin outside the allowlist.
- A behavior change hidden in the port. Filters, search semantics, validation rules and error cases match today's admin.

## Today's admin, to match

Pages under `frontend/src/routes/admin/`, data in `admin.remote.ts`:

| Page | Reads | Actions |
| --- | --- | --- |
| Overview `/` | Source and post counts, posts in 24h, flagged sources, Brave searches in 24h against the 322 budget, discovered link counts by status | none |
| Sources `/sources` | All sources with domain, adapter, status, rate, post count, last post. `?deleted` includes deleted ones | Sync an active enabled source. Keep or Disable a flagged enabled source |
| New source `/sources/new` | Listing check of a URL: feed and page candidates, WordPress total | Create a source from a chosen candidate, rate and language |
| Tags `/tags` | Tags with terms and post counts, proposals with support, aliases | Enable or disable a tag. Merge a tag into another |
| Posts `/posts` | Posts filtered by source, tag and full-text search, 50 per page with a total | none |
| Post `/posts/:id` | One post with content and source URL | none |
| Logs `/logs` | Logs filtered by type, level and a top-level metadata key and value, 50 per page, no total | none |
| Log `/logs/:id` | One log with request and response bodies | none |
| Login `/login` | none | Log in |

Every link between pages stays: a source's post count to its posts, a tag to its posts, a post's tag or domain to a filtered list, a log's type or metadata pair to a filtered list.

## Design

### Go API

All routes are JSON with camelCase keys, under the existing `/admin/` mux on the Go app. Errors are plain text with a status, as the current routes do.

| Method and path | Body or query | Answer |
| --- | --- | --- |
| `POST /admin/login` | `{email, password}` | 204 and the session cookie, or 401 |
| `POST /admin/logout` | none | 204, deletes the session and clears the cookie |
| `GET /admin/session` | none | 204 when the cookie is valid, else 401 |
| `GET /admin/stats` | none | Overview counts, with the link counts by status |
| `GET /admin/sources` | `deleted=true` | Sources as today |
| `POST /admin/sources` | `{url, adapter, listingUrl, rate, lang}` | 201, 400 on validation, 409 when the listing URL already has a live source |
| `POST /admin/sources/{id}/review` | `{enabled}` | 204 or 404 |
| `POST /admin/sources/{id}/sync` | none | Exists. Unchanged |
| `GET /admin/listing` | `url` | Exists. Unchanged |
| `GET /admin/posts` | `source`, `tag`, `search`, `page` | `{posts, total, page, pageSize}` |
| `GET /admin/posts/{id}` | none | One post, or 404 |
| `GET /admin/tags` | none | `{tags, proposals, aliases}` |
| `PATCH /admin/tags/{key}` | `{enabled}` | 204 |
| `POST /admin/tags/merge` | `{from, to}` | 204, 400 when from equals to, 404 for an unknown target |
| `GET /admin/logs` | `type`, `level`, `key`, `value`, `page` | `{logs, hasNext, page}` |
| `GET /admin/logs/{id}` | none | One log with bodies, or 404 |

The SQL moves from `admin.remote.ts` into postgres stores, behind the same filters and limits. Post search keeps the same three-language full-text match plus tag term match. The admin post list is its own query, not the public `PostStore.ListPosts`. That one only filters on enabled active tags, sorts differently and has no source filter or total. It would hide posts behind proposed and disabled tags. Builder's call. Creating a source keeps the domain upsert and source insert in one transaction, and normalizes the domain with the Go `domain.BaseURL`.

Validation matches today's valibot rules: `url` is an http(s) URL, `adapter` is `rss` or `page`, `listingUrl` is an http(s) URL, `rate` is an integer of at least 1, `lang` is empty, `sv` or `en`, tag keys are 1 to 40 characters, ids are UUIDs, log ids are digits, search is at most 500 characters and a metadata key at most 100.

### Sessions

The schema stays: `users` with scrypt hashes, `sessions` with the sha256 of the token.

- Login verifies the scrypt hash in the format `frontend/src/lib/server/password.ts` writes: hex salt, colon, hex 64-byte key, Node's defaults N 16384, r 8, p 1. An unknown email still runs one verification, so it takes as long as a wrong password. On success it deletes expired sessions and inserts a new one for 30 days.
- The token is 32 random bytes, base64url. It goes out as cookie `session`: HttpOnly, Secure, SameSite Strict, Path `/admin`, Max-Age 30 days, host-only on the API host.
- `auth.RequireSession` reads the cookie instead of the bearer header. Nothing sends the bearer header once the web app's admin is gone.
- The SPA calls `GET /admin/session` before rendering an admin page, and goes to `/login` on any 401.

The admin and API hosts are both under `dagulv.me`, so they are the same site and a Strict cookie still goes with the SPA's credentialed requests. In dev, `localhost:5173` and `localhost:3001` are also the same site, and browsers accept Secure cookies on localhost.

### CORS

A middleware in front of `/admin/` answers for origins in a new `ADMIN_ORIGINS` env, comma separated. It defaults to `http://localhost:5173`, and prod sets `https://admin.kharoborathus.dagulv.me`.

- An allowed origin gets `Access-Control-Allow-Origin` with that exact origin, `Access-Control-Allow-Credentials: true` and `Vary: Origin`.
- Preflight `OPTIONS` answers 204 before the session check, allowing `GET, POST, PATCH` and the `Content-Type` header.
- Writes require `Content-Type: application/json`, so a cross-origin form post cannot reach them without a preflight. Body-less writes, like sync and logout, send an empty JSON body.

### Users

`frontend/scripts/add-user.ts` moves to the Go binary as `kharoborathus user add <email>`, next to `migrate`. It generates the password the same way and writes the same hash format.

### SPA

`admin/` at the repo root is a new SvelteKit project with `adapter-static`, `ssr = false` and an `index.html` fallback. It uses the same toolchain and versions as `frontend/`: pnpm, Svelte 5 runes, Tailwind 4, TypeScript, prettier, eslint, valibot. Remote functions do not work without a server, so data goes through a small typed fetch client with `credentials: 'include'`. The API base comes from `PUBLIC_API_URL` at build time.

Routes drop the `/admin` prefix, since the SPA has its own host: `/`, `/sources`, `/sources/new`, `/tags`, `/posts`, `/posts/[id]`, `/logs`, `/logs/[id]`, `/login`. Filters live in the query string as today, so a filtered list can be linked.

The types in `frontend/src/lib/types.ts` move to `admin/src/lib/types.ts`, with dates parsed from JSON strings.

### Look

shadcn-svelte components, added with `pnpm dlx shadcn-svelte@latest add`. The theme follows Linear:

- A fixed left sidebar with the five sections and logout, collapsing to a sheet on narrow screens.
- Compact density: 13px body text, 32px controls, tight table rows, thin borders, small radii.
- Neutral grays with one indigo accent, near Linear's `#5E6AD2`.
- Inter, with tabular numbers in counts and durations.
- Light and dark, following the system.

Statuses are badges. Overview counts are cards. Lists are shadcn tables. Action feedback such as Sync, Keep, Disable and Merge may use toasts in place of today's inline text, as long as success and failure both show.

### Web app cleanup

The SvelteKit web app loses `routes/admin`, `routes/login`, `lib/server/auth.ts`, `lib/server/password.ts`, `scripts/add-user.ts`, the `user:add` script, the admin and remote-function branches of `hooks.server.ts`, and any env such as `API_URL` that only the admin used. The healthz answer stays. The public page and `/posts` route stay as they are.

### Deploy

`pnpm build` in `admin/` writes static files to `admin/build`, with `PUBLIC_API_URL=https://api.kharoborathus.dagulv.me`.

A new job in `.github/workflows/deploy.yml` runs when `admin/` changes, with the same push-to-main rule as the other targets. Pull requests only build. On main it builds, uploads `admin/build` to a Bunny Storage zone, removes files the build no longer has, and purges the pull zone. The storage zone name, its password and the account API key for the purge come from the GitHub `production` environment and the prod vault, next to the existing secrets. The builder checks the Bunny Storage and purge APIs in Bunny's docs rather than guessing them.

The pull zone serves the storage zone at `admin.kharoborathus.dagulv.me` and rewrites unknown paths to `/index.html`. `infra/README.md` documents the zones, the rewrite, the secrets and the build env. The `api` target in `infra/haloy.yaml` gets `ADMIN_ORIGINS`.

The switch ships as one change. The cookie replaces the bearer header and the web admin is removed in the same push.

### Tests

- Go route tests through `dbtest`: login success and failure, a protected route with no, bad and valid cookie, CORS preflight from allowed and other origins, and each read and write route's main and error cases.
- The scrypt verification against a hash made by today's Node code.
- `pnpm check` and `pnpm lint` pass in `admin/` and `frontend/`.

## Left to the builder

- Store and package layout on the Go side, within the existing `adapter/postgres` and `core/service` split.
- Whether the SPA loads data in `+page.ts` load functions or in components.
- Which shadcn-svelte components to use, and the exact theme tokens.
- Whether `/admin/tags` stays one route or splits per section.
