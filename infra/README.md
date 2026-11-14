# Infra

Runs the stack on one UpCloud server with Haloy. Design: [docs/kb/infra.md](../docs/kb/infra.md).

## Secrets

Prod secrets never sit in an env file. They live in two vaults under `secrets/`, encrypted with sops to one age key kept in Bitwarden. Each service's `<service>.env` holds safe values only, and is committed:

| Service | Dev env | Prod env | Deploy Dockerfile |
|---|---|---|---|
| db | `infra/db/db.env`, password from the root `.env` | `infra/db/db.env` | `infra/db/Dockerfile` |
| api, the Go app | The root `.env` over `infra/api/api.env` over `infra/db/db.env` | `infra/api/api.env` | `infra/api/Dockerfile` |
| web, the public site | The root `.env` over `infra/web/web.env` | `infra/web/web.env` | `infra/web/Dockerfile` |
| admin, a static SPA on bunny.net | `PUBLIC_API_URL`, default `http://localhost:3001` | `PUBLIC_API_URL` at build time, set in CI | none, see [Admin](#admin) |

The dev db also builds from `infra/db/Dockerfile`.

| Vault | File | Keys |
|---|---|---|
| dev | `kharoborathus/app.yaml` | `POSTGRES_PASSWORD`, `DEEPSEEK_API_KEY`, `TYPESAFE_API_KEY`, `BRAVE_API_KEY` |
| prod | `kharoborathus/app.yaml` | The dev keys plus `POSTGRES_CONNECTION_STRING` |
| prod | `kharoborathus/tofu.yaml` | `UPCLOUD_TOKEN`, `TF_VAR_state_passphrase` |
| prod | `kharoborathus/bunny.yaml` | `BUNNY_STORAGE_ZONE`, `BUNNY_STORAGE_HOST`, `BUNNY_STORAGE_PASSWORD`, `BUNNY_API_KEY`, `BUNNY_PULL_ZONE_ID` |

Each file is a Kubernetes `Secret` with the keys under `stringData`. [.sops.yaml](.sops.yaml) encrypts only `data` and `stringData`, so key names stay readable. Nothing here deploys to a cluster.

`secrets.sh` runs a command with one vault's secrets in its environment. It asks for the Bitwarden master password, decrypts in memory and locks Bitwarden again. `prod:tofu`, `prod:app` or `prod:bunny` limits it to one file, so each command sees only the secrets it needs:

```bash
infra/secrets.sh dev docker compose up -d db
infra/secrets.sh dev go run ./cmd migrate
infra/secrets.sh dev go run ./cmd
infra/secrets.sh dev pnpm -C frontend dev
pnpm -C admin dev
```

To skip the prompt in dev, `setup.sh` writes the dev vault's secrets into the root `.env` and keeps its other lines. Compose, the Go app and the admin read that file, so `docker compose up -d db`, `go run ./cmd migrate`, `go run ./cmd` and `pnpm -C frontend dev` then work bare. Rerun it after a vault change:

```bash
infra/setup.sh
```

Change secrets with sops from inside `infra/`. sops finds `.sops.yaml` by searching upward from the working directory, and new files need its rule. First load the age key from Bitwarden:

```bash
export BW_SESSION=$(bw unlock --raw)
export SOPS_AGE_KEY=$(bw get item 4aa7bc48-6193-458f-b276-b4e700bb4ffe | jq -r '.fields[] | select(.name == "AGE-SECRET-KEY") | .value')
```

Edit a file. sops decrypts it in `$EDITOR` and encrypts it again on save:

```bash
sops edit secrets/prod/vault.sealed/kharoborathus/app.yaml
```

Add a file by writing the `Secret` manifest in plain text under `secrets/<vault>/vault.sealed/kharoborathus/`, then encrypting it in place. Check its values read `ENC[...]` before committing:

```bash
sops encrypt --in-place secrets/prod/vault.sealed/kharoborathus/tofu.yaml
```

Then drop the key:

```bash
unset SOPS_AGE_KEY BW_SESSION
bw lock
```

Running containers keep their old env, and CI does not redeploy for a vault change. Redeploy the targets that use the changed keys.

## First time

1. Install the Haloy CLI, v0.1.0-beta.73 to match the server:

   ```bash
   go install github.com/haloydev/haloy/cmd/haloy@v0.1.0-beta.73
   ```

2. Create an UpCloud API token under Account > API Tokens, limited to your IP. It lasts at most 365 days, so renew it before it expires. Add it as `UPCLOUD_TOKEN` to the prod vault's `tofu.yaml` with sops, with a state passphrase of at least 16 characters.

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

7. Add the prod vault's `app.yaml` with sops. The safe values are already committed in each `<service>.env`.

   Generate `POSTGRES_PASSWORD` with `openssl rand -hex 32` and put the same value in `POSTGRES_CONNECTION_STRING`. Characters like `/`, `@` or `$` break the URL or get expanded.

8. In the GitHub repo, create the `production` environment with three secrets: `HALOY_API_TOKEN` from `ssh deploy@<ip> sudo haloyd config get api-token --raw`, the prod `POSTGRES_PASSWORD`, and `SOPS_AGE_KEY` from the Bitwarden field `AGE-SECRET-KEY`. The age key opens both vaults, so CI can read every prod secret, the UpCloud token included.

9. Deploy the database first. Once its logs say it accepts connections, run the CI/CD workflow by hand. A manual run migrates, then deploys `api` and `web`:

   ```bash
   infra/secrets.sh prod:app infra/haloy.sh deploy db
   infra/secrets.sh prod:app infra/haloy.sh logs db
   gh workflow run deploy.yml --ref main
   gh run watch
   ```

   `haloy.sh` runs Haloy once per target from that target's directory, with only that target's `<target>.env` in its env. Any Haloy command that loads the config resolves every env value, so `logs` and `status` need `secrets.sh` too.

## Later

- A push to `main` runs the [CI/CD workflow](../.github/workflows/deploy.yml). It deploys `api` when Go code, `go.mod`, `infra/api` or `haloy.yaml` changed, and `web` when `frontend`, `infra/web` or `haloy.yaml` changed. Pull requests only build those images. `db` never deploys from CI.
- Redeploy by hand with `infra/secrets.sh prod:app infra/haloy.sh deploy api web`, or `gh workflow run deploy.yml --ref main` for both. Images build where Haloy runs and upload to the server.
- Logs: `infra/secrets.sh prod:app infra/haloy.sh logs api`. `infra/secrets.sh prod:app infra/haloy.sh rollback-targets api` lists deployment IDs, and `infra/secrets.sh prod:app infra/haloy.sh rollback api -- <id>` rolls back to one.
- Schema changes go in a new numbered file in `internal/migrations`. Never edit a file that has been applied. `go run ./cmd migrate` applies pending files with tern and records the version in `schema_version`. Tern runs each file through Go's `text/template`, so `{{` in SQL breaks it.
- A push to `main` that touches `internal/migrations` migrates prod first, through `haloy tunnel` to the db. The running api still has the old code until its deploy finishes, so a migration must work with it. A failed migration stops the deploy.
- A dev volume created before the migrations existed has the schema but no `schema_version`, so `migrate` fails on it. Recreate it once with `docker compose down -v`.
- Add an admin user with `POSTGRES_CONNECTION_STRING=<conn> go run ./cmd user add <email>`. It prints a generated password: the login has no rate limit or lockout, so only its length stops guessing.
- Every switch starts off. Turn them on in the admin under Switches.
- `tofu apply` never replaces the server: it has `prevent_destroy`, and edits to `cloud-init.yaml` or the SSH key are ignored for the existing server.

## Admin

The admin is a static SvelteKit SPA in `admin/`, served by bunny.net at `admin.kharoborathus.dagulv.me`. It reads and writes only through the Go app's `/admin/*` routes. The Go app sets the session cookie on `api.kharoborathus.dagulv.me` and answers CORS for the origins in `ADMIN_ORIGINS`, which `haloy.yaml` sets for prod and which defaults to `http://localhost:5173` in dev.

Set up once by hand:

1. In bunny.net, create a storage zone. Note its name, its storage hostname, such as `storage.bunnycdn.com`, and its password.
2. Create a pull zone with the storage zone as its origin. Note its ID.
3. On the pull zone, add an edge rule that rewrites requests for missing files to `/index.html`, so deep links reach the SPA.
4. Add `admin.kharoborathus.dagulv.me` as a hostname on the pull zone, point a CNAME at the pull zone's hostname and enable its certificate.
5. Add the prod vault's `bunny.yaml` with sops, holding the five `BUNNY_*` keys above. `BUNNY_API_KEY` is the account API key, which the cache purge needs.

A push to `main` that touches `admin/` or `infra/admin/` builds the SPA with `PUBLIC_API_URL=https://api.kharoborathus.dagulv.me`. [upload.sh](admin/upload.sh) then mirrors `admin/build` into the storage zone, deletes files the build no longer has and purges the pull zone. Pull requests only build. Upload by hand with:

```bash
PUBLIC_API_URL=https://api.kharoborathus.dagulv.me pnpm -C admin build
infra/secrets.sh prod:bunny bash infra/admin/upload.sh admin/build
```
