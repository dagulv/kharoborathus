# Infra

**Status:** done
**Related:** [architecture](architecture.md), [rollout](rollout.md)

## Summary

Runs the whole stack on one UpCloud VPS. OpenTofu creates the server and cloud-init prepares it. Haloy runs Postgres, the Go app and the admin as containers and serves HTTPS.

## Current state

- [deploy/tofu](../../deploy/tofu) creates the server. It has not been applied or run through `tofu validate` yet.
- [deploy/haloy.yaml](../../deploy/haloy.yaml) runs the stack. It passes `haloy validate-config` but has not been deployed. The admin's auth hook answered 401 without credentials and 200 on `/healthz` in a local run.
- [deploy/README.md](../../deploy/README.md) lists the first-time steps.
- [docker-compose.yaml](../../docker-compose.yaml) runs the dev database, now with `schema.sql` baked into the image.

## Design

Tools:

| Tool | Job | Why |
|---|---|---|
| OpenTofu with the `UpCloudLtd/upcloud` provider | Creates the server and its backups | Infra as code, as asked. |
| cloud-init | Installs Docker, a firewall, haloyd and a `deploy` user on first boot | Ships with the Ubuntu template, so no extra tool is needed. |
| Haloy v0.1.0-beta.73 | Builds, ships and runs the three services. Its proxy serves HTTPS | One config file replaces kustomize overlays, with zero-downtime deploys, rollbacks and certificates and no registry. |

Kustomize, kubectl and kubectx are left out. Kustomize only renders Kubernetes manifests, and this setup has no cluster.

Layout:

1. `deploy/tofu` holds the OpenTofu config and the cloud-init template.
2. `deploy/haloy.yaml` holds the production stack and its domains. `deploy/.env` holds its secrets.
3. `deploy/api.Dockerfile` and `frontend/Dockerfile` build the Go app and the admin. `docker/postgres/Dockerfile` builds the database with the Swedish dictionary and `schema.sql`.

Server:

- Ubuntu 24.04 LTS template, metadata service on, one public interface.
- Daily UpCloud storage backups, kept 7 days. They cover the Postgres volume.
- A `deploy` user with your SSH key. Root login and password login are off. Cloud-init copies the Haloy API token to its home.
- ufw allows SSH, HTTP and HTTPS in and denies the rest. haloy-proxy binds 80 and 443 on the host and no container publishes a port, so ufw's known Docker bypass exposes nothing.
- Unattended security upgrades are on.
- The server has `prevent_destroy` and ignores later changes to `user_data` and `login`, since the provider replaces the server for them and Postgres lives on its disk. Changes to `cloud-init.yaml` apply only to new servers.
- Docker's default log driver is the rotating `local` driver, so logs cannot fill the disk.

Why not the UpCloud firewall: it is stateless on public interfaces. Scraping arbitrary sites would need rules that accept any inbound packet from ports 80 and 443. ufw is stateful.

Why not managed Postgres: `schema.sql` needs the `sv_se` ispell dictionary files in the server's `tsearch_data` directory. A managed service cannot load them.

Routing in Haloy:

Haloy's proxy routes by hostname only. It has no path rules, no auth and no compression.

- The API domain goes to the Go app's public port 3001, which serves only `/feeds` and `/feeds/{id}`. This is the public API.
- The site domain goes to the admin. The admin checks basic auth itself in `hooks.server.ts`, against `ADMIN_USER` and `ADMIN_PASSWORD`. It has no login of its own, and its remote functions live under `/_app/remote`, not `/admin`. So the whole site is protected, not only `/admin`. `/healthz` stays open for Haloy's health check.
- `/sources/{id}/sync` lives on the Go app's internal port 3002, which no proxy publishes. Only the admin calls it, at `api:3002` on the `haloy` network.
- The Go app uses static naming, so its container is always called `api`. That forces the replace strategy, which also keeps two scrapers from overlapping. Feeds are down for a few seconds per Go deploy. The admin deploys rolling.
- Responses are not compressed.

Deploy:

1. `tofu apply` in `deploy/tofu` creates the server. You point the site, API and Haloy API domains at its IPv4 output.
2. `haloy server add` registers the server with the token cloud-init left in the deploy user's home.
3. `haloy deploy` builds the images locally and uploads them to haloyd, so no registry is needed. The `db` target is protected and deploys only by name.
4. Postgres runs `schema.sql` on its first start with an empty volume. Later schema changes need migrations, see [rollout](rollout.md).

Build notes:

- `frontend/pnpm-lock.yaml` pins tarballs to a local registry proxy at `127.0.0.1:11230`. The admin image rewrites those URLs to `registry.npmjs.org`, and the integrity hashes still verify them.
- SvelteKit validates env during the build, so the build passes a placeholder connection string. The real one is read at runtime.

Secrets:

- UpCloud credentials come from `UPCLOUD_USERNAME` and `UPCLOUD_PASSWORD`. They never enter the config.
- The OpenTofu state is encrypted with a passphrase from `TF_VAR_state_passphrase`. It stays local and gitignored.
- App secrets live in `deploy/.env`, which is gitignored. Haloy reads it on your machine and passes each value `haloy.yaml` names to its container.
- The Haloy API token lives in `~/.config/haloy` locally and in `/etc/haloy` on the server.

## Schema changes

None.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Zone | `se-sto1` | Stockholm, close to Swedish sources and readers. |
| Plan | `2xCPU-4GB` | Postgres, two app processes and Haloy fit with headroom. |
| Disk | 50 GB | Posts are text. Grows with `size`. |
| Backups | Daily, 7 kept | UpCloud storage backups, no extra tooling. |

## Open questions

- Off-site Postgres dumps, for example to UpCloud Managed Object Storage, once the data is worth more than a week of backups.
