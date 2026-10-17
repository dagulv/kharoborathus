#!/usr/bin/env bash
# Runs a command with one vault's secrets in its environment: infra/secrets.sh <dev|prod> <command...>
# Decrypts in memory with the vault's age key from Bitwarden, as the vault tool does. A set SOPS_AGE_KEY skips Bitwarden.
set -euo pipefail
# fetch_key runs in a command substitution, which drops errexit without this.
shopt -s nullglob inherit_errexit

if [[ $# -lt 2 || ! $1 =~ ^[a-z]+$ ]]; then
	echo "usage: $0 <vault> <command...>" >&2
	exit 2
fi

vault=$1
shift
root=$(cd "$(dirname "$0")" && pwd)
config=$root/.sops.yaml

fetch_key() {
	# Locks again however this subshell exits.
	trap 'bw lock >/dev/null 2>&1' EXIT
	local password session
	read -rsp "Bitwarden master password for vault $vault: " password </dev/tty
	echo >&2
	# Env, not argv, so ps never shows the password or the session.
	# bw dumps several SDK errors for a wrong password, so say it in one line.
	if ! session=$(BW_MASTER_PASSWORD=$password bw unlock --passwordenv BW_MASTER_PASSWORD --raw --nointeraction 2>/dev/null); then
		echo "Bitwarden unlock failed, likely a wrong master password" >&2
		exit 1
	fi
	if ! BW_SESSION=$session bw get item "$1" --nointeraction | jq -er '.fields[] | select(.name == "AGE-SECRET-KEY") | .value'; then
		echo "no AGE-SECRET-KEY field readable on Bitwarden item $1" >&2
		exit 1
	fi
}

if ! sealed=$(yq -e ".vaults.$vault.sealed" "$config" 2>/dev/null); then
	echo "no vault $vault in $config" >&2
	exit 2
fi

key=${SOPS_AGE_KEY:-}
if [[ -z $key ]]; then
	key=$(fetch_key "$(yq -e ".vaults.$vault.bitwarden.item" "$config")")
fi

files=("$root/$sealed"/*/*.yaml)
if [[ ${#files[@]} -eq 0 ]]; then
	echo "vault $vault has no sealed secrets" >&2
	exit 1
fi

for file in "${files[@]}"; do
	lines=$(SOPS_AGE_KEY=$key sops decrypt --extract '["stringData"]' --output-type dotenv "$file")
	while IFS= read -r line; do
		[[ -n $line ]] && export "$line"
	done <<<"$lines"
done

exec "$@"
