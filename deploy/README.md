# Deploy

Runs the stack on one UpCloud server. Design: [docs/kb/infra.md](../docs/kb/infra.md).

## First time

1. Create an UpCloud API user. Export its credentials and a state passphrase of at least 16 characters:

   ```bash
   export UPCLOUD_USERNAME=... UPCLOUD_PASSWORD=... TF_VAR_state_passphrase=...
   ```

2. Put your SSH public key in `terraform.tfvars`. The server ignores later key changes, so a wrong key locks you out:

   ```bash
   cd deploy/tofu
   cp terraform.tfvars.example terraform.tfvars
   ```

3. Create the server:

   ```bash
   tofu init
   tofu apply
   ```

   Keep `terraform.tfstate`: it is encrypted with the passphrase and gitignored, so back it up yourself.

4. Point the domain's A record at `tofu output -raw ipv4`. Caddy gets the certificate on the first request.

5. Write the app config:

   ```bash
   cp deploy/production.env.example deploy/production.env
   docker run --rm caddy:2-alpine caddy hash-password --plaintext '<admin password>'
   ```

   Put the hash in `ADMIN_PASSWORD_HASH` inside single quotes. Fill in the Postgres password in both places and the API keys.

6. Wait for cloud-init, then deploy:

   ```bash
   ssh deploy@<ip> cloud-init status --wait
   deploy/deploy.sh
   ```

## Later

- Redeploy with `deploy/deploy.sh`. It reads the IP from the encrypted state, so export `TF_VAR_state_passphrase` first, or set `DEPLOY_HOST=<ip>`. Images are rebuilt on the server.
- Logs: `DOCKER_HOST=ssh://deploy@<ip> docker compose -f deploy/compose.yaml logs -f api`.
- `schema.sql` runs only on an empty database volume. Schema changes after the first deploy need migrations.
- Every switch starts off. Turn them on in the admin under Switches.
- `tofu apply` never replaces the server: it has `prevent_destroy`, and edits to `cloud-init.yaml` or the SSH key are ignored for the existing server.
