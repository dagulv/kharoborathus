# Infra on UpCloud Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy the whole stack to one UpCloud VPS with OpenTofu, cloud-init, Docker Compose and Caddy.

**Architecture:** OpenTofu in `deploy/tofu` creates one Ubuntu 24.04 server with daily storage backups and a cloud-init config that installs Docker, ufw and a `deploy` user. `deploy/compose.yaml` runs Postgres (with `schema.sql` baked in), the Go app, the SvelteKit admin and Caddy. Caddy serves HTTPS, routes `/feeds` to the Go app and puts everything else behind basic auth. `deploy/deploy.sh` builds and starts the stack over `DOCKER_HOST=ssh://`.

**Tech Stack:** OpenTofu ≥ 1.8, provider `UpCloudLtd/upcloud ~> 5.47`, cloud-init, Docker Compose v2, Caddy 2, Go 1.27, Node 24 with pnpm.

**Spec:** `docs/kb/infra.md`. User request: OpenTofu, UpCloud, VPS, no Kubernetes, kubectl or kubectx.

## Global Constraints

- No Kubernetes tooling. Kustomize is left out because it only renders Kubernetes manifests (ruling below).
- Secrets never enter git or OpenTofu state: UpCloud credentials from `UPCLOUD_USERNAME` and `UPCLOUD_PASSWORD`, the state passphrase from `TF_VAR_state_passphrase`, app secrets in gitignored `deploy/production.env`.
- Never run `tofu apply` or create cloud resources. Never print `.env` values.
- Do not touch the dev database volume. Local smoke tests use a separate compose project and volume, removed afterwards.
- One commit per purpose, ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.

## Plan rulings

- **Kustomize is not used.** It renders Kubernetes manifests, and you ruled out Kubernetes. A production compose file is the VPS equivalent of an overlay.
- **No extra tools beyond OpenTofu, cloud-init, Docker Compose and Caddy.** Caddy is the one addition: automatic HTTPS without certbot cron jobs.
- **ufw instead of the UpCloud firewall.** The UpCloud public firewall is stateless. Outbound scraping would need inbound rules for any packet from ports 80 and 443.
- **Self-hosted Postgres instead of managed.** `schema.sql` needs the `sv_se` ispell files in `tsearch_data`.
- **The whole admin sits behind basic auth.** It has no login and its remote functions live under `/_app/remote`. Only `/feeds` is public.
- **Images are built on the server** through `DOCKER_HOST=ssh://`, so no registry or remote git is needed. The repo has no remote.
- **Bind mounts are avoided** in production compose, since with a remote Docker host they resolve on the server. The Caddyfile is baked into a small image.
- **The dev database image also bakes in `schema.sql`.** A fresh dev volume then starts with the schema. Existing volumes are untouched, since init scripts only run on an empty data directory.
- **OpenTofu state stays local and encrypted** with OpenTofu's state encryption. A remote backend would need storage that does not exist yet.
- **OpenTofu could not be validated here** because the `tofu` command is blocked in this session. The config follows the provider docs for v5.47 and the OpenTofu encryption docs, and must be checked with `tofu init && tofu validate` before the first apply.

## Review Focus

- Nothing but `/feeds` may be reachable without auth, including `/_app/remote` and `/sources/{id}/sync`. Pinned in Task 2 smoke test.
- Postgres must never be published on the host. Pinned in Task 2 (`docker compose config`, no `ports` on `db`).
- A bcrypt hash with `$` must survive the env file. Pinned in Task 2 smoke test (login works).
- A fresh database must come up with the schema and all four switches off. Pinned in Task 2 smoke test.
- Secrets must not be committed. Pinned in Task 1 and 3 (`.gitignore`, `git status` checks).

---

### Task 1: Container images

**Files:**
- Create: `.dockerignore`, `deploy/api.Dockerfile`, `frontend/Dockerfile`, `frontend/.dockerignore`
- Modify: `docker/postgres/Dockerfile`, `docker-compose.yaml`, `.gitignore`

- [ ] **Step 1: Root `.dockerignore`**

```
.git
.idea
.superpowers
docs
frontend
deploy/tofu
deploy/production.env
.env
.env-sample
```

- [ ] **Step 2: Postgres image with the schema**

Append to `docker/postgres/Dockerfile`:

```dockerfile

# Runs only on an empty data directory
COPY internal/migrations/schema.sql /docker-entrypoint-initdb.d/schema.sql
```

In `docker-compose.yaml` change `build: docker/postgres` to:

```yaml
    build:
      context: .
      dockerfile: docker/postgres/Dockerfile
```

- [ ] **Step 3: Go app image** `deploy/api.Dockerfile`:

```dockerfile
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kharoborathus ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kharoborathus /kharoborathus
EXPOSE 3001
ENTRYPOINT ["/kharoborathus"]
```

- [ ] **Step 4: Admin image** `frontend/Dockerfile` and `frontend/.dockerignore` (`node_modules`, `build`, `.svelte-kit`):

```dockerfile
FROM node:24-alpine AS build
RUN corepack enable
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build && pnpm prune --prod

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/package.json ./
COPY --from=build /app/node_modules node_modules
COPY --from=build /app/build build
USER node
EXPOSE 3000
CMD ["node", "build"]
```

- [ ] **Step 5: Build all three**

Run: `docker build -f deploy/api.Dockerfile -t kh-api:check .`, `docker build -t kh-web:check frontend`, `docker build -f docker/postgres/Dockerfile -t kh-db:check .`
Expected: all succeed. Then remove the `:check` tags.

- [ ] **Step 6: Commit** per purpose: `Bake the schema into the database image`, `Add container images for the Go app and the admin`.

---

### Task 2: Production stack

**Files:**
- Create: `deploy/compose.yaml`, `deploy/Caddyfile`, `deploy/caddy.Dockerfile`, `deploy/production.env.example`
- Modify: `.gitignore` (add `deploy/production.env`)

- [ ] **Step 1: Caddyfile**

```
{$SITE_DOMAIN} {
	encode zstd gzip

	@feeds path /feeds /feeds/*
	handle @feeds {
		reverse_proxy api:3001
	}

	# The admin has no login and its remote functions live outside /admin, so the whole site needs auth.
	handle {
		basic_auth {
			{$ADMIN_USER} {$ADMIN_PASSWORD_HASH}
		}
		reverse_proxy web:3000
	}
}
```

`deploy/caddy.Dockerfile`:

```dockerfile
FROM caddy:2-alpine
COPY Caddyfile /etc/caddy/Caddyfile
```

- [ ] **Step 2: Compose**

```yaml
name: kharoborathus

services:
  db:
    build:
      context: ..
      dockerfile: docker/postgres/Dockerfile
    restart: unless-stopped
    env_file: production.env
    volumes:
      - db:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U \"$$POSTGRES_USER\" -d \"$$POSTGRES_DB\""]
      interval: 5s
      timeout: 5s
      retries: 20

  api:
    build:
      context: ..
      dockerfile: deploy/api.Dockerfile
    restart: unless-stopped
    env_file: production.env
    environment:
      HTTP_HOST: 0.0.0.0:3001
    depends_on:
      db:
        condition: service_healthy

  web:
    build: ../frontend
    restart: unless-stopped
    env_file: production.env
    environment:
      API_URL: http://api:3001
    depends_on:
      db:
        condition: service_healthy

  caddy:
    build:
      context: .
      dockerfile: caddy.Dockerfile
    restart: unless-stopped
    env_file: production.env
    ports:
      - 80:80
      - 443:443
      - 443:443/udp
    volumes:
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      - api
      - web

volumes:
  db:
  caddy_data:
  caddy_config:
```

- [ ] **Step 3: Env example** `deploy/production.env.example`:

```
# Copy to production.env, which is gitignored.
SITE_DOMAIN=news.example.com
ORIGIN=https://news.example.com

ADMIN_USER=admin
# docker run --rm caddy:2-alpine caddy hash-password --plaintext '<password>'
# Keep the single quotes, or compose expands the $ signs in the hash.
ADMIN_PASSWORD_HASH='<bcrypt hash>'

POSTGRES_USER=kharoborathus
POSTGRES_PASSWORD=<password>
POSTGRES_DB=kharoborathus
POSTGRES_CONNECTION_STRING=postgresql://kharoborathus:<password>@db:5432/kharoborathus?sslmode=disable

DEEPSEEK_API_KEY=
TYPESAFE_API_KEY=
BRAVE_API_KEY=
```

- [ ] **Step 4: Static checks**

Run: `docker compose --env-file deploy/production.env.example -f deploy/compose.yaml config` with a copy of the example as `production.env`.
Expected: valid, and `db` has no `ports`.

- [ ] **Step 5: Local smoke test**

In the scratchpad, write `smoke.env` from the example with `SITE_DOMAIN=http://localhost:8088`, `ORIGIN=http://localhost:8088`, a fresh hash for a throwaway password, throwaway Postgres credentials and `DEEPSEEK_API_KEY` copied from `.env` without printing it. Override caddy ports to `8088:8088` only. Run under project name `kh_smoke` and check:
- `curl -s -o /dev/null -w '%{http_code}' localhost:8088/feeds` → `200`
- `/`, `/admin`, `/_app/remote/x`, `/sources/x/sync` without auth → `401`
- `/admin` with the throwaway credentials → `200`
- `SELECT key, enabled FROM switches` in the `db` container → four rows, all `f`

Then `docker compose -p kh_smoke down -v` and delete `smoke.env`.

- [ ] **Step 6: Commit** `Add the production compose stack behind Caddy`.

---

### Task 3: OpenTofu and cloud-init

**Files:**
- Create: `deploy/tofu/versions.tf`, `deploy/tofu/variables.tf`, `deploy/tofu/main.tf`, `deploy/tofu/outputs.tf`, `deploy/tofu/cloud-init.yaml`, `deploy/tofu/terraform.tfvars.example`
- Modify: `.gitignore` (add `deploy/tofu/.terraform/`, `deploy/tofu/*.tfstate*`, `deploy/tofu/terraform.tfvars`, `deploy/tofu/*.tfplan`)

Contents as committed. Key points: provider `UpCloudLtd/upcloud ~> 5.47`, `required_version >= 1.8.0`, state and plan encryption with `pbkdf2` and `aes_gcm` from `var.state_passphrase` (sensitive, at least 16 characters), `upcloud_server` with `metadata = true`, Ubuntu 24.04 template, `backup_rule` daily at 0200 kept 7, public interface, `login { user = "deploy", keys = [var.ssh_public_key], create_password = false, password_delivery = "none" }`, `user_data = file("cloud-init.yaml")`. Output `ipv4`.

cloud-init installs `docker.io`, `docker-buildx`, `docker-compose-v2`, `ufw`, `unattended-upgrades`, writes an sshd drop-in that disables root and password login, adds `deploy` to `docker`, and enables ufw with SSH, 80/tcp, 443/tcp and 443/udp.

- [ ] **Step 1:** Write the files.
- [ ] **Step 2:** Parse `cloud-init.yaml` with Python's `yaml` module. Expected: a dict with `#cloud-config` as the first line.
- [ ] **Step 3:** `tofu fmt -check` and `tofu validate` if the `tofu` command is allowed, otherwise note it in the ledger.
- [ ] **Step 4: Commit** `Provision the UpCloud server with OpenTofu`.

---

### Task 4: Deploy script and docs

**Files:**
- Create: `deploy/deploy.sh`, `deploy/README.md`
- Modify: `docs/kb/infra.md` (status `done`, current state), `docs/kb/index.md`, `docs/kb/architecture.md`

`deploy.sh`:

```bash
#!/usr/bin/env bash
# Builds the images on the server and starts the stack. DEPLOY_HOST overrides the OpenTofu output.
set -euo pipefail

cd "$(dirname "$0")"

host=${DEPLOY_HOST:-$(tofu -chdir=tofu output -raw ipv4)}

DOCKER_HOST="ssh://deploy@${host}" docker compose --env-file production.env -f compose.yaml up -d --build --remove-orphans
```

`deploy/README.md` gives the first-time steps: credentials, `tofu init`, `tofu apply`, DNS A record, `production.env`, `./deploy.sh`, and how to read logs with `DOCKER_HOST=ssh://deploy@<ip> docker compose -f compose.yaml logs -f api`.

- [ ] **Step 1:** Write the files. `bash -n deploy/deploy.sh`, `chmod +x`.
- [ ] **Step 2:** Update the kb pages.
- [ ] **Step 3: Commit** `Add the deploy script and infra docs`.
