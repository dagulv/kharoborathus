#!/usr/bin/env bash
# Runs the Go app locally with the dev vault: infra/api/dev.sh
# The app reads the first .env up from its working directory, so running from here picks infra/api/.env.
set -euo pipefail

cd "$(dirname "$0")"
exec ../secrets.sh dev go run ../../cmd "$@"
