#!/usr/bin/env bash
# Writes the dev vault's secrets into the repo root .env, so local services read them without secrets.sh: infra/setup.sh
# Replaces only the vault's keys and keeps every other line. Rerun after a vault change.
set -euo pipefail

root=$(cd "$(dirname "$0")" && pwd)
env=$root/../.env
# Key names are not encrypted, so yq reads them without Bitwarden.
mapfile -t keys < <(yq -e '.stringData | keys | .[]' "$root"/secrets/dev/vault.sealed/*/*.yaml)

umask 077
tmp=$(mktemp "$env.XXXXXX")
trap 'rm -f "$tmp"' EXIT

if [[ -f $env ]]; then
	grep -vE "^($(IFS='|' && echo "${keys[*]}"))=" "$env" >"$tmp" || true
fi
# Unquoted, since the Go app's .env parser keeps quotes as part of the value.
"$root/secrets.sh" dev bash -c 'for k; do printf "%s=%s\n" "$k" "${!k}"; done' _ "${keys[@]}" >>"$tmp"
mv "$tmp" "$env"
