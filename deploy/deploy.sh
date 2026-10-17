#!/usr/bin/env bash
# Builds the images on the server and starts the stack. DEPLOY_HOST overrides the OpenTofu output.
set -euo pipefail

cd "$(dirname "$0")"

host=${DEPLOY_HOST:-$(tofu -chdir=tofu output -raw ipv4)}

DOCKER_HOST="ssh://deploy@${host}" docker compose --env-file production.env -f compose.yaml up -d --build --remove-orphans
