# Infra

**Status:** done
**Related:** [architecture](architecture.md), [rollout](rollout.md)

## Summary

Runs the whole stack on one UpCloud VPS. OpenTofu creates the server and cloud-init prepares it. Haloy runs Postgres, the Go app and the admin as containers and serves HTTPS.

## Current state

- [infra/tofu](../../infra/tofu) creates the server. It has not been applied or run through `tofu validate` yet.
- [infra/haloy.yaml](../../infra/haloy.yaml) runs the stack. It passes `haloy validate-config` but has not been deployed. The admin's auth hook answered 401 without credentials and 200 on `/healthz` in a local run.
- [infra/README.md](../../infra/README.md) lists the first-time steps.
- [docker-compose.yaml](../../docker-compose.yaml) runs the dev database, now with `schema.sql` baked into the image.

## Design

Tools:

| Tool | Job | Why |
|---|---|---|
| OpenTofu with the `UpCloudLtd/upcloud` provider | Creates the server and its backups | Infra as code, as asked. |
| cloud-init | Installs Docker, a firewall, haloyd and a `deploy` user on first boot | Ships with the Ubuntu template, so no extra tool is needed. |
| Haloy v0.1.0-beta.73 | Builds, ships and runs the three services. Its proxy serves HTTPS | One config file replaces kustomize overlays, with zero-downtime deploys, rollbacks and certificates and no registry. |
| SOPS with age, through the `vault` tool | Encrypts secrets in git | Same flow as the other infra repos. The age key lives in Bitwarden, never on disk. |

Kustomize, kubectl and kubectx are left out. Kustomize only renders Kubernetes manifests, and this setup has no cluster.

Layout:

1. `infra/tofu` holds the OpenTofu config and the cloud-init template.
2. `infra/haloy.yaml` holds the production stack and its domains. `infra/haloy.sh` runs one Haloy command per target.
3. `infra/.sops.yaml` and `infra/secrets/{dev,prod}/vault.sealed` hold the encrypted secrets. `infra/secrets.sh` runs a command with one vault's secrets in its environment.
4. `infra/<service>/` holds each service's deploy `Dockerfile` and prod `.env`, for `db`, `api` and `web`. `infra/db/Dockerfile` builds the database with the Swedish dictionary and `schema.sql`, for dev too. The `api` and `web` images build from the repo root and from `frontend/`.
5. Dev env files sit with the code: `.env` for the Go app, `frontend/.env` for the admin and `docker/postgres/.env` for the dev database.

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

1. `tofu apply` in `infra/tofu` creates the server. You point the site, API and Haloy API domains at its IPv4 output.
2. `haloy server add` registers the server with the token cloud-init left in the deploy user's home.
3. `secrets.sh prod haloy.sh deploy <target>...` builds the images locally and uploads them to haloyd, so no registry is needed. The `db` target is protected and deploys only by name.
4. Haloy has no per-target env file. It reads the working directory's `.env`, and a `.env.<target>` only for targets named with `-t`, all into one process env. So `haloy.sh` runs Haloy once per target from `infra/<target>/`, and each target resolves only its own env. Every Haloy command that loads the config resolves all its env values, including `logs` and `status`.
5. Postgres runs `schema.sql` on its first start with an empty volume. Later schema changes need migrations, see [rollout](rollout.md).

Build notes:

- `frontend/pnpm-lock.yaml` pins tarballs to a local registry proxy at `127.0.0.1:11230`. The admin image rewrites those URLs to `registry.npmjs.org`, and the integrity hashes still verify them.
- SvelteKit validates env during the build, so the build passes a placeholder connection string. The real one is read at runtime.

Secrets:

- No secret sits in an env file. Each service's env file holds its safe values: user names, database names, hosts and ports.
- Secrets live in the `dev` and `prod` SOPS vaults, one Kubernetes `Secret` manifest per file, since that is the format `vault` manages. Only `data` and `stringData` are encrypted. Both vaults share one age key, kept in a Bitwarden custom field `AGE-SECRET-KEY`. `vault` requires a cluster name, so they name `none`.
- `secrets.sh` unlocks Bitwarden with the master password, decrypts every file of one vault in memory and exports its `stringData` keys, then locks Bitwarden and runs the command. The password and session go through env, never argv. A set `SOPS_AGE_KEY` skips Bitwarden.
- The Go app and Vite both let the process env win over their `.env`, and both expand `${POSTGRES_PASSWORD}` in a `.env` value from it. The dev Postgres container reads `POSTGRES_PASSWORD` from the process env too.
- UpCloud credentials: `UPCLOUD_USERNAME` is a safe value you export. `UPCLOUD_PASSWORD` and `TF_VAR_state_passphrase` live in the prod vault's `tofu.yaml`. The OpenTofu state stays local, encrypted and gitignored.
- Haloy passes each value `haloy.yaml` names to its container. The rest of the prod vault never leaves your machine.
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
| Disk encryption | On | Free, and backups inherit it. Costs about 10-20% of disk speed. It can only be set when the server is created. |

## Open questions

- Off-site Postgres dumps, for example to UpCloud Managed Object Storage, once the data is worth more than a week of backups.
