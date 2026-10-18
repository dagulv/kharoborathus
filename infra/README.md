# Infra

Runs the stack on one UpCloud server with Haloy. Design: [docs/kb/infra.md](../docs/kb/infra.md).

## Secrets

Prod secrets never sit in an env file. They live in two SOPS vaults under `secrets/`, managed with the `vault` tool and encrypted to one age key kept in Bitwarden. `.env` files hold safe values only, one per service:

| Service | Dev env | Prod env | Deploy Dockerfile |
|---|---|---|---|
| db | `infra/db/.env`, password from the root `.env` | `infra/db/.env` | `infra/db/Dockerfile` |
| api, the Go app | The root `.env` over `infra/api/.env` over `infra/db/.env` | `infra/api/.env` | `infra/api/Dockerfile` |
| web, the admin | The root `.env` over `infra/web/.env` over `infra/db/.env` | `infra/web/.env` | `infra/web/Dockerfile` |

Each has a `.env-sample` or `.env.example` beside it. The dev db also builds from `infra/db/Dockerfile`.

| Vault | File | Keys |
|---|---|---|
| dev | `kharoborathus/app.yaml` | `POSTGRES_PASSWORD`, `DEEPSEEK_API_KEY`, `TYPESAFE_API_KEY`, `BRAVE_API_KEY` |
| prod | `kharoborathus/app.yaml` | The dev keys plus `POSTGRES_CONNECTION_STRING` |
| prod | `kharoborathus/tofu.yaml` | `UPCLOUD_TOKEN`, `TF_VAR_state_passphrase` |

Each file is a Kubernetes `Secret` with the keys under `stringData`, since that is what `vault` manages. Nothing here deploys to a cluster, so `vault diff` and `vault apply` don't apply.

`secrets.sh` runs a command with one vault's secrets in its environment. It asks for the Bitwarden master password, decrypts in memory and locks Bitwarden again. `prod:tofu` or `prod:app` limits it to one file, so each command sees only the secrets it needs:

```bash
infra/secrets.sh dev docker compose up -d db
infra/secrets.sh dev go run ./cmd migrate
infra/secrets.sh dev go run ./cmd
infra/secrets.sh dev pnpm -C frontend dev
```

To skip the prompt in dev, `setup.sh` writes the dev vault's secrets into the root `.env` and keeps its other lines. Compose, the Go app and the admin read that file, so `docker compose up -d db`, `go run ./cmd migrate`, `go run ./cmd` and `pnpm -C frontend dev` then work bare. Rerun it after a vault change:

```bash
infra/setup.sh
```

Change a secret from inside `infra/`:

```bash
vault edit secrets/prod/vault.sealed/kharoborathus/app.yaml
```

Add one by writing the manifest to the open directory, for example `secrets/prod/vault.open/kharoborathus/tofu.yaml`, then running `vault seal --vault prod`.

## First time

1. Install the Haloy CLI, v0.1.0-beta.73 to match the server:

   ```bash
   go install github.com/haloydev/haloy/cmd/haloy@v0.1.0-beta.73
   ```

2. Create an UpCloud API token under Account > API Tokens, limited to your IP. It lasts at most 365 days, so renew it before it expires. Seal it as `UPCLOUD_TOKEN` in the prod vault's `tofu.yaml`, with a state passphrase of at least 16 characters.

3. Put your SSH public key and the Haloy API domain in `terraform.tfvars`. The server ignores later changes to either, so a wrong key locks you out:

   ```bash
   cp infra/tofu/terraform.tfvars.example infra/tofu/terraform.tfvars
   ```

4. Create the server:

   ```bash
   tofu -chdir=infra/tofu init
   infra/secrets.sh prod:tofu tofu -chdir=infra/tofu apply
   ```

   Keep `terraform.tfstate`: it is encrypted with the passphrase and gitignored, so back it up yourself.

5. Point three A records at `infra/secrets.sh prod:tofu tofu -chdir=infra/tofu output -raw ipv4`: `kharoborathus.dagulv.me`, `api.kharoborathus.dagulv.me` and `haloy.kharoborathus.dagulv.me`. Haloy requests the certificates from Let's Encrypt, which fails until the records resolve. Cloud-init waits up to two hours for the Haloy record before it installs haloyd.

6. Wait for cloud-init, then register the server:

   ```bash
   ssh deploy@<ip> cloud-init status --wait
   haloy server add haloy.kharoborathus.dagulv.me "$(ssh deploy@<ip> cat haloy-token)"
   ssh deploy@<ip> rm haloy-token
   ```

   Haloy takes the token only as an argument, so it shows briefly in your local process list. The copy on the server is deleted once it is registered.

   If cloud-init timed out on DNS, haloyd retries its certificate only every 12 hours. Restart it once the records resolve: `ssh deploy@<ip> sudo systemctl restart haloyd`.

7. Copy the safe values, then seal the prod vault's `app.yaml`:

   ```bash
   for t in db api web; do cp infra/$t/.env.example infra/$t/.env; done
   ```

   Generate `POSTGRES_PASSWORD` with `openssl rand -hex 32` and put the same value in `POSTGRES_CONNECTION_STRING`. Characters like `/`, `@` or `$` break the URL or get expanded.

8. Deploy the database first, then the rest once its logs say it accepts connections. The database starts empty and `api` needs the schema, so migrate in between. Prod has no migrate step yet: it is planned as a CI job.

   ```bash
   infra/secrets.sh prod:app infra/haloy.sh deploy db
   infra/secrets.sh prod:app infra/haloy.sh logs db
   infra/secrets.sh prod:app infra/haloy.sh deploy api web
   ```

   `haloy.sh` runs Haloy once per target from that target's directory. Haloy reads the working directory's `.env`, so each target sees only its own env. Any Haloy command that loads the config resolves every env value, so `logs` and `status` need `secrets.sh` too.

## Later

- Redeploy with `infra/secrets.sh prod:app infra/haloy.sh deploy api web`. Images build locally and upload to the server. Leave `db` out unless its image changed.
- Logs: `infra/secrets.sh prod:app infra/haloy.sh logs api`. `infra/secrets.sh prod:app infra/haloy.sh rollback-targets api` lists deployment IDs, and `infra/secrets.sh prod:app infra/haloy.sh rollback api -- <id>` rolls back to one.
- Schema changes go in a new numbered file in `internal/migrations`. Never edit a file that has been applied. `go run ./cmd migrate` applies pending files with tern and records the version in `schema_version`. Tern runs each file through Go's `text/template`, so `{{` in SQL breaks it.
- A dev volume created before the migrations existed has the schema but no `schema_version`, so `migrate` fails on it. Recreate it once with `docker compose down -v`.
- Add an admin user with `POSTGRES_CONNECTION_STRING=<conn> pnpm user:add <email>` from `frontend`. It prints a generated password: the login has no rate limit or lockout, so only its length stops guessing.
- Every switch starts off. Turn them on in the admin under Switches.
- `tofu apply` never replaces the server: it has `prevent_destroy`, and edits to `cloud-init.yaml` or the SSH key are ignored for the existing server.
