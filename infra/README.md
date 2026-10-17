# Infra

Runs the stack on one UpCloud server with Haloy. Design: [docs/kb/infra.md](../docs/kb/infra.md).

## Secrets

Secrets never sit in an env file. They live in two SOPS vaults under `secrets/`, managed with the `vault` tool and encrypted to one age key kept in Bitwarden. `.env` files hold safe values only.

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

5. Point three A records at `infra/secrets.sh prod tofu -chdir=infra/tofu output -raw ipv4`: the site, the API and the Haloy API domain. Haloy gets the certificates on the first request.

6. Wait for cloud-init, then register the server:

   ```bash
   ssh deploy@<ip> cloud-init status --wait
   haloy server add <haloy api domain> "$(ssh deploy@<ip> cat haloy-token)"
   ```

7. Set your domains in `infra/haloy.yaml`. Copy the safe values, then seal the prod vault's `app.yaml`:

   ```bash
   cp infra/.env.example infra/.env
   ```

8. Deploy the database first, then the rest once its logs say it accepts connections:

   ```bash
   cd infra
   ./secrets.sh prod haloy deploy -t db
   haloy logs -t db
   ./secrets.sh prod haloy deploy --all
   ```

   Run Haloy from `infra/`. It reads the `.env` in the working directory before the one beside `haloy.yaml`, so from the repo root it would pick up dev values.

## Later

- Redeploy from `infra/` with `./secrets.sh prod haloy deploy --all`. Images build locally and upload to the server. `--all` skips the protected `db` target.
- Logs: `haloy logs -t api`, from `infra/`. `haloy rollback-targets` lists deployment IDs for `haloy rollback <id>`.
- `schema.sql` runs only on an empty database volume. Schema changes after the first deploy need migrations.
- Every switch starts off. Turn them on in the admin under Switches.
- `tofu apply` never replaces the server: it has `prevent_destroy`, and edits to `cloud-init.yaml` or the SSH key are ignored for the existing server.
