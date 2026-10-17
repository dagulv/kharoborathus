# Infra

Runs the stack on one UpCloud server with Haloy. Design: [docs/kb/infra.md](../docs/kb/infra.md).

## Secrets

Secrets never sit in an env file. They live in two SOPS vaults under `secrets/`, managed with the `vault` tool and encrypted to one age key kept in Bitwarden. `.env` files hold safe values only, one per service:

| Service | Dev env | Prod env | Deploy Dockerfile |
|---|---|---|---|
| db | `docker/postgres/.env` | `infra/db/.env` | `infra/db/Dockerfile` |
| api, the Go app | `.env` | `infra/api/.env` | `infra/api/Dockerfile` |
| web, the admin | `frontend/.env` | `infra/web/.env` | `infra/web/Dockerfile` |

Each has a `.env-sample` or `.env.example` beside it. The dev db also builds from `infra/db/Dockerfile`.

| Vault | File | Keys |
|---|---|---|
| dev | `kharoborathus/app.yaml` | `POSTGRES_PASSWORD`, `ADMIN_PASSWORD`, `DEEPSEEK_API_KEY`, `TYPESAFE_API_KEY`, `BRAVE_API_KEY` |
| prod | `kharoborathus/app.yaml` | The dev keys plus `POSTGRES_CONNECTION_STRING` |
| prod | `kharoborathus/tofu.yaml` | `UPCLOUD_PASSWORD`, `TF_VAR_state_passphrase` |

Each file is a Kubernetes `Secret` with the keys under `stringData`, since that is what `vault` manages. Nothing here deploys to a cluster, so `vault diff` and `vault apply` don't apply.

`secrets.sh` runs a command with one vault's secrets in its environment. It asks for the Bitwarden master password, decrypts in memory and locks Bitwarden again:

```bash
infra/secrets.sh dev docker compose up -d db
infra/secrets.sh dev go run ./cmd
infra/secrets.sh dev pnpm -C frontend dev
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

2. Create an UpCloud API user. Seal its password and a state passphrase of at least 16 characters in the prod vault's `tofu.yaml`. Export the username:

   ```bash
   export UPCLOUD_USERNAME=...
   ```

3. Put your SSH public key and the Haloy API domain in `terraform.tfvars`. The server ignores later changes to either, so a wrong key locks you out:

   ```bash
   cp infra/tofu/terraform.tfvars.example infra/tofu/terraform.tfvars
   ```

4. Create the server:

   ```bash
   tofu -chdir=infra/tofu init
   infra/secrets.sh prod tofu -chdir=infra/tofu apply
   ```

   Keep `terraform.tfstate`: it is encrypted with the passphrase and gitignored, so back it up yourself.

5. Point three A records at `infra/secrets.sh prod tofu -chdir=infra/tofu output -raw ipv4`: `kharoborathus.dagulv.me`, `api.kharoborathus.dagulv.me` and `haloy.kharoborathus.dagulv.me`. Haloy requests the certificates from Let's Encrypt, which fails until the records resolve, so wait for them before step 6.

6. Wait for cloud-init, then register the server:

   ```bash
   ssh deploy@<ip> cloud-init status --wait
   haloy server add haloy.kharoborathus.dagulv.me "$(ssh deploy@<ip> cat haloy-token)"
   ```

7. Copy the safe values, then seal the prod vault's `app.yaml`:

   ```bash
   for t in db api web; do cp infra/$t/.env.example infra/$t/.env; done
   ```

   Generate `POSTGRES_PASSWORD` with `openssl rand -hex 32` and put the same value in `POSTGRES_CONNECTION_STRING`. Characters like `/`, `@` or `$` break the URL or get expanded.

8. Deploy the database first, then the rest once its logs say it accepts connections:

   ```bash
   infra/secrets.sh prod infra/haloy.sh deploy db
   infra/secrets.sh prod infra/haloy.sh logs db
   infra/secrets.sh prod infra/haloy.sh deploy api web
   ```

   `haloy.sh` runs Haloy once per target from that target's directory. Haloy reads the working directory's `.env`, so each target sees only its own env. Any Haloy command that loads the config resolves every env value, so `logs` and `status` need `secrets.sh` too.

## Later

- Redeploy with `infra/secrets.sh prod infra/haloy.sh deploy api web`. Images build locally and upload to the server. Leave `db` out unless its image changed.
- Logs: `infra/secrets.sh prod infra/haloy.sh logs api`. `rollback-targets` through `haloy.sh` lists deployment IDs for `haloy rollback <id>`.
- `schema.sql` runs only on an empty database volume. Schema changes after the first deploy need migrations.
- Every switch starts off. Turn them on in the admin under Switches.
- `tofu apply` never replaces the server: it has `prevent_destroy`, and edits to `cloud-init.yaml` or the SSH key are ignored for the existing server.
