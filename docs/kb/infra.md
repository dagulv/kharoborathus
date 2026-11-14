# Infra

**Status:** done
**Related:** [architecture](architecture.md), [rollout](rollout.md)

## Summary

Runs the whole stack on one UpCloud VPS. OpenTofu creates the server and cloud-init prepares it. Haloy runs Postgres, the Go app and the public web app as containers and serves HTTPS. The admin is a static SPA on bunny.net.

## Current state

- [infra/tofu](../../infra/tofu) creates the server. It has not been applied or run through `tofu validate` yet.
- [infra/haloy.yaml](../../infra/haloy.yaml) runs the stack. It passes `haloy validate-config` but has not been deployed.
- [infra/README.md](../../infra/README.md) lists the first-time steps.
- [docker-compose.yaml](../../docker-compose.yaml) runs the dev database, with the Swedish dictionary baked into the image. `go run ./cmd migrate` creates the schema.

## Design

Tools:

| Tool | Job | Why |
|---|---|---|
| OpenTofu with the `UpCloudLtd/upcloud` provider | Creates the server and its backups | Infra as code, as asked. |
| cloud-init | Installs Docker, a firewall, haloyd and a `deploy` user on first boot | Ships with the Ubuntu template, so no extra tool is needed. |
| Haloy v0.1.0-beta.73 | Builds, ships and runs the three services. Its proxy serves HTTPS | One config file replaces kustomize overlays, with zero-downtime deploys, rollbacks and certificates and no registry. |
| SOPS with age | Encrypts secrets in git | Same format as the other infra repos. The age key lives in Bitwarden, never on disk. |

Kustomize, kubectl and kubectx are left out. Kustomize only renders Kubernetes manifests, and this setup has no cluster.

Layout:

1. `infra/tofu` holds the OpenTofu config and the cloud-init template.
2. `infra/haloy.yaml` holds the production stack and its domains. `infra/haloy.sh` runs one Haloy command per target.
3. `infra/.sops.yaml` and `infra/secrets/{dev,prod}/vault.sealed` hold the encrypted secrets. `infra/secrets.sh` runs a command with one vault's secrets in its environment.
4. `infra/<service>/` holds each service's deploy `Dockerfile` and its committed safe values in `<service>.env`, for `db`, `api` and `web`. `infra/db/Dockerfile` builds the database with the Swedish dictionary, for dev too. The `api` and `web` images build from the repo root and from `frontend/`.
5. Dev reads each service's `infra/<service>/<service>.env` too, with the root `.env` over it for secrets and dev overrides. The Go config also reads `infra/db/db.env` under its own file for the database names, and compose reads `infra/db/db.env` for the container and the root `.env` for the password. When nothing sets `POSTGRES_CONNECTION_STRING`, the Go app builds it from the user, password and database at `localhost:5432`.

Server:

- Ubuntu 24.04 LTS template, metadata service on, one public interface.
- Daily UpCloud storage backups, kept 7 days. They cover the Postgres volume.
- A `deploy` user with your SSH key. Root login and password login are off. Cloud-init copies the Haloy API token to its home.
- ufw allows SSH, HTTP and HTTPS in and denies the rest. haloy-proxy binds 80 and 443 on the host and no container publishes a port, so ufw's known Docker bypass exposes nothing.
- Unattended security upgrades are on.
- Cloud-init installs haloyd v0.1.0-beta.73 from the installer at that release tag and checks the installer and both binaries against pinned SHA-256 hashes. A Haloy upgrade means updating the version and the three hashes together.
- The server has `prevent_destroy` and ignores later changes to `user_data` and `login`, since the provider replaces the server for them and Postgres lives on its disk. Changes to `cloud-init.yaml` apply only to new servers.
- Docker's default log driver is the rotating `local` driver, so logs cannot fill the disk.

Why not the UpCloud firewall: it is stateless on public interfaces. Scraping arbitrary sites would need rules that accept any inbound packet from ports 80 and 443. ufw is stateful.

Why not managed Postgres: the schema needs the `sv_se` ispell dictionary files in the server's `tsearch_data` directory. A managed service cannot load them.

Routing in Haloy:

Haloy's proxy routes by hostname only. It has no path rules, no auth and no compression.

- The API domain goes to the Go app's only port, 3001. `/posts` and `/posts/{id}` are the public API. Everything under `/admin/` but `POST /admin/login` needs the `session` cookie. Login checks an email and password against the `users` table and sets a random token as an HttpOnly, Secure, SameSite Strict cookie on the API host with path `/admin`. The `sessions` table holds only the token's sha256, so a database leak gives no usable cookie. CORS answers only the origins in `ADMIN_ORIGINS`, and writes must be JSON, so a cross-site form cannot reach them.
- The admin SPA at `admin.kharoborathus.dagulv.me` is static files in a Bunny Storage zone behind a pull zone. It is on the same site as the API, so the Strict cookie still goes with its requests.
- The site domain serves the public feed at `/`, paged by `GET /posts?after=<post id>` on the Go app at `api:3001` over the `haloy` network. `/healthz` stays open for Haloy's health check.
- The Go app uses static naming, so its container is always called `api`. That forces the replace strategy, which also keeps two scrapers from overlapping. Feeds are down for a few seconds per Go deploy. The web app deploys rolling.
- Responses are not compressed.

Deploy:

1. `tofu apply` in `infra/tofu` creates the server. You point the site, API and Haloy API domains at its IPv4 output.
2. `haloy server add` registers the server with the token cloud-init left in the deploy user's home.
3. `secrets.sh prod:app haloy.sh deploy <target>...` builds the images where it runs and uploads them to haloyd, so no registry is needed. The [CI/CD workflow](../../.github/workflows/deploy.yml) runs it for `api` and `web` on pushes to `main` that change them, and pull requests only build those images. It also builds the admin SPA and uploads it to bunny.net on pushes that change `admin/`. The `db` target is protected and deploys only by name, by hand.
4. Haloy has no per-target env file, and it only reads files named `.env`. So `haloy.sh` runs Haloy once per target from `infra/<target>/`, with that target's `<target>.env` exported into its env, and each target resolves only its own values. A key already in the env, like a secret from `secrets.sh`, wins. Haloy passes a container only the values `haloy.yaml` names, so `haloy.sh deploy` refuses a target whose `<target>.env` holds a key that `haloy.yaml` does not pass. Every Haloy command that loads the config resolves all its env values, including `logs` and `status`. Haloy also loads `infra/.env` and `.env.local` from the working directory, `infra/` and `~/.config/haloy`, and `.env.local` overrides the vault. `.gitignore` keeps `.env` and every `.env.*` out of git, and none of those files should exist under `infra/`.
5. The database starts empty. `kharoborathus migrate` applies the numbered files in `internal/migrations` with tern. In prod the CI/CD workflow runs it on every push to `main` that touches `internal/migrations`, before `api` and `web` deploy. A failed migration skips the deploy. It reaches the db through `haloy tunnel` over the haloyd API, so it needs no SSH and no published port.

Build notes:

- `frontend/pnpm-lock.yaml` and `admin/pnpm-lock.yaml` pin tarballs to a local registry proxy at `127.0.0.1:11230`. The web image and the admin CI job rewrite those URLs to `registry.npmjs.org`, and the integrity hashes still verify them.

Secrets:

- No prod secret sits in an env file. Each service's env file holds its safe values: user names, database names, hosts and ports. In dev, `infra/setup.sh` may write the dev vault's secrets into the root `.env`, which is gitignored and mode 600.
- Secrets live in the `dev` and `prod` SOPS vaults, one Kubernetes `Secret` manifest per file, edited with `sops edit`. Only `data` and `stringData` are encrypted. Both vaults share one age key, kept in a Bitwarden custom field `AGE-SECRET-KEY`. `secrets.sh` reads each vault's sealed directory and Bitwarden item from the `vaults:` section of `.sops.yaml`, which sops ignores.
- `secrets.sh` unlocks Bitwarden with the master password, decrypts every file of one vault, or the one file named after a colon, in memory and exports its `stringData` keys, then locks Bitwarden and runs the command. The password and session go through env, never argv. A set `SOPS_AGE_KEY` skips Bitwarden and is unset before the command runs.
- The Go app and Vite both let the process env win over their env files. The Go app expands `${POSTGRES_PASSWORD}` in `POSTGRES_CONNECTION_STRING`, and Vite expands only in the root `.env`. The dev Postgres container reads `POSTGRES_PASSWORD` from the process env too.
- UpCloud credentials: an API token, not a user's password, since it expires within a year, can be limited to your IP and revoked alone. `UPCLOUD_TOKEN` and `TF_VAR_state_passphrase` live in the prod vault's `tofu.yaml`. The OpenTofu state stays local, encrypted and gitignored.
- Haloy passes each value `haloy.yaml` names to its container. CI decrypts the prod vault with `SOPS_AGE_KEY`, so the age key, and with it every vault secret, is also a GitHub secret.
- The Haloy API token lives in `~/.config/haloy` locally and in `/etc/haloy` on the server.
- The CI/CD workflow needs `HALOY_API_TOKEN`, `POSTGRES_PASSWORD` and `SOPS_AGE_KEY` as secrets of the GitHub `production` environment, with the prod values. Without the first two Haloy cannot resolve the db target's env and the migration fails before the tunnel opens. Deploys need the age key for `secrets.sh`.

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
