#!/usr/bin/env bash
# Runs a haloy command once per target, from the target's own directory: infra/haloy.sh <command> <target>... [-- <args>...]
# Haloy reads the working directory's .env, so each target sees only its own. Wrap it in secrets.sh prod:app.
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

for target in "${targets[@]}"; do
	(cd "$root/$target" && haloy "$command" "$@" -c ../haloy.yaml -t "$target")
done
