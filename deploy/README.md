# Deploy

Runs the stack on one UpCloud server with Haloy. Design: [docs/kb/infra.md](../docs/kb/infra.md).

## First time

1. Install the Haloy CLI, v0.1.0-beta.73 to match the server:

   ```bash
   go install github.com/haloydev/haloy/cmd/haloy@v0.1.0-beta.73
   ```

2. Create an UpCloud API user. Export its credentials and a state passphrase of at least 16 characters:

   ```bash
   export UPCLOUD_USERNAME=... UPCLOUD_PASSWORD=... TF_VAR_state_passphrase=...
   ```

3. Put your SSH public key and the Haloy API domain in `terraform.tfvars`. The server ignores later changes to either, so a wrong key locks you out:

   ```bash
   cd deploy/tofu
   cp terraform.tfvars.example terraform.tfvars
   ```

4. Create the server:

   ```bash
   tofu init
   tofu apply
   ```

   Keep `terraform.tfstate`: it is encrypted with the passphrase and gitignored, so back it up yourself.

5. Point three A records at `tofu output -raw ipv4`: the site, the API and the Haloy API domain. Haloy gets the certificates on the first request.

6. Wait for cloud-init, then register the server:

   ```bash
   ssh deploy@<ip> cloud-init status --wait
   haloy server add <haloy api domain> "$(ssh deploy@<ip> cat haloy-token)"
   ```

7. Set your domains in `deploy/haloy.yaml`, then write the app config:

   ```bash
   cp deploy/.env.example deploy/.env
   ```

   Fill in the admin password, the Postgres password in both places and the API keys.

8. Deploy the database first, then the rest once its logs say it accepts connections:

   ```bash
   haloy deploy -c deploy/haloy.yaml -t db
   haloy logs -c deploy/haloy.yaml -t db
   haloy deploy -c deploy/haloy.yaml --all
   ```

## Later

- Redeploy with `haloy deploy -c deploy/haloy.yaml --all`. Images build locally and upload to the server. `--all` skips the protected `db` target.
- Logs: `haloy logs -c deploy/haloy.yaml -t api`. `haloy rollback-targets` lists deployment IDs for `haloy rollback <id>`.
- `schema.sql` runs only on an empty database volume. Schema changes after the first deploy need migrations.
- Every switch starts off. Turn them on in the admin under Switches.
- `tofu apply` never replaces the server: it has `prevent_destroy`, and edits to `cloud-init.yaml` or the SSH key are ignored for the existing server.
