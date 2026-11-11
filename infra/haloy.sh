#!/usr/bin/env bash
# Runs a haloy command once per target, from the target's own directory: infra/haloy.sh <command> <target>... [-- <args>...]
# Each target's haloy call gets only its own <target>/<target>.env. Wrap it in secrets.sh prod:app.
# Arguments after -- go to every haloy call, for example a deployment ID: haloy.sh rollback api -- <id>
set -euo pipefail

if [[ $# -lt 2 ]]; then
	echo "usage: $0 <command> <target>... [-- <args>...]" >&2
	exit 2
fi

command=$1
shift
root=$(cd "$(dirname "$0")" && pwd)

targets=()
while [[ $# -gt 0 && $1 != -- ]]; do
	targets+=("$1")
	shift
done

if [[ $# -gt 0 ]]; then
	shift
fi

# Haloy passes a container only the env haloy.yaml names, so a key added to <target>.env alone never reaches it.
if [[ $command == deploy ]]; then
	for target in "${targets[@]}"; do
		[[ -f $root/$target/$target.env ]] || continue
		missing=$(comm -23 \
			<(grep -oE '^[A-Za-z_][A-Za-z0-9_]*=' "$root/$target/$target.env" | tr -d = | sort -u) \
			<(yq ".targets.$target.env[].name" "$root/haloy.yaml" | sort -u))
		if [[ -n $missing ]]; then
			echo "$target/$target.env has keys haloy.yaml does not pass to $target:" $missing >&2
			exit 1
		fi
	done
fi

# Haloy only reads a file named .env, so the target's file goes in through the env instead.
# Values stay unquoted and a key the env already has wins, like the Go app's .env parser.
for target in "${targets[@]}"; do
	(
		cd "$root/$target"
		if [[ -f $target.env ]]; then
			# The || keeps a last line that has no newline.
			while IFS='=' read -r key value || [[ -n $key ]]; do
				if [[ ! $key =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
					continue
				fi
				if [[ -z ${!key+set} ]]; then
					export "$key=$value"
				fi
			done <"$target.env"
		fi
		haloy "$command" "$@" -c ../haloy.yaml -t "$target"
	)
done
