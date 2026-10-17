# Infra

**Status:** done
**Related:** [architecture](architecture.md), [rollout](rollout.md)

## Summary

Runs the whole stack on one UpCloud VPS. OpenTofu creates the server and cloud-init prepares it. Docker Compose runs Postgres, the Go app, the admin and Caddy, which serves HTTPS.

## Current state

- [deploy/tofu](../../deploy/tofu) creates the server. It has not been applied or run through `tofu validate` yet.
- [deploy/compose.yaml](../../deploy/compose.yaml) and [deploy/Caddyfile](../../deploy/Caddyfile) run the stack. A local smoke run served `/feeds` openly, answered 401 everywhere else without auth, and started Postgres with the schema and every switch off.
- [deploy/deploy.sh](../../deploy/deploy.sh) deploys. [deploy/README.md](../../deploy/README.md) lists the first-time steps.
- [docker-compose.yaml](../../docker-compose.yaml) runs the dev database, now with `schema.sql` baked into the image.

## Design

Tools:

| Tool | Job | Why |
|---|---|---|
| OpenTofu with the `UpCloudLtd/upcloud` provider | Creates the server and its backups | Infra as code, as asked. |
| cloud-init | Installs Docker, a firewall and a `deploy` user on first boot | Ships with the Ubuntu template, so no extra tool is needed. |
| Docker Compose | Runs the four services | Already used in dev. Plain compose files replace kustomize overlays, since there is no Kubernetes. |
| Caddy | HTTPS and routing | Gets certificates by itself, with no extra tooling. |

Kustomize, kubectl and kubectx are left out. Kustomize only renders Kubernetes manifests, and this setup has no cluster.

Layout:

1. `deploy/tofu` holds the OpenTofu config and the cloud-init template.
2. `deploy/compose.yaml` holds the production stack. `deploy/Caddyfile` holds routing.
3. `deploy/api.Dockerfile` and `deploy/web.Dockerfile` build the Go app and the admin. `docker/postgres/Dockerfile` builds the database with the Swedish dictionary and `schema.sql`.
4. `deploy/deploy.sh` builds and starts the stack on the server over SSH.

Server:

- Ubuntu 24.04 LTS template, metadata service on, one public interface.
- Daily UpCloud storage backups, kept 7 days. They cover the Postgres volume.
- A `deploy` user with your SSH key, in the `docker` group. Root login and password login are off.
- ufw allows SSH, HTTP and HTTPS in and denies the rest. Docker publishes only Caddy's ports 80 and 443, so ufw's known Docker bypass exposes nothing else.
- Unattended security upgrades are on.

Why not the UpCloud firewall: it is stateless on public interfaces. Scraping arbitrary sites would need rules that accept any inbound packet from ports 80 and 443. ufw is stateful.

Why not managed Postgres: `schema.sql` needs the `sv_se` ispell dictionary files in the server's `tsearch_data` directory. A managed service cannot load them.

Routing in Caddy:

- `/feeds` and `/feeds/*` go to the Go app. This is the public API.
- Everything else goes to the admin, behind basic auth. The admin has no login of its own, and its remote functions live under `/_app/remote`, not `/admin`. So the whole site is protected, not only `/admin`.
- `/sources/{id}/sync` is never routed. Only the admin calls it, over the internal network.

Deploy:

1. `tofu apply` in `deploy/tofu` creates the server. You point your domain's A record at its IPv4 output.
2. `deploy/deploy.sh` runs `docker compose` against `ssh://deploy@<ip>`. Images are built on the server from the local checkout, so no registry is needed.
3. Postgres runs `schema.sql` on its first start with an empty volume. Later schema changes need migrations, see [rollout](rollout.md).

Build notes:

- `frontend/pnpm-lock.yaml` pins tarballs to a local registry proxy at `127.0.0.1:11230`. The admin image rewrites those URLs to `registry.npmjs.org`, and the integrity hashes still verify them.
- SvelteKit validates env during the build, so the build passes a placeholder connection string. The real one is read at runtime.

Secrets:

- UpCloud credentials come from `UPCLOUD_USERNAME` and `UPCLOUD_PASSWORD`. They never enter the config.
- The OpenTofu state is encrypted with a passphrase from `TF_VAR_state_passphrase`. It stays local and gitignored.
- App secrets live in `deploy/production.env`, which is gitignored. Compose reads it on your machine and passes the values to the containers.

## Schema changes

None.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Zone | `se-sto1` | Stockholm, close to Swedish sources and readers. |
| Plan | `2xCPU-4GB` | Postgres, two app processes and Caddy fit with headroom. |
| Disk | 50 GB | Posts are text. Grows with `size`. |
| Backups | Daily, 7 kept | UpCloud storage backups, no extra tooling. |

## Open questions

- Off-site Postgres dumps, for example to UpCloud Managed Object Storage, once the data is worth more than a week of backups.
