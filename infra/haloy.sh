#!/usr/bin/env bash
# Runs a haloy command once per target, from the target's own directory: infra/haloy.sh <command> <target>...
# Haloy reads the working directory's .env, so each target sees only its own. Wrap it in secrets.sh prod.
set -euo pipefail

if [[ $# -lt 2 ]]; then
	echo "usage: $0 <command> <target>..." >&2
	exit 2
fi

command=$1
shift
root=$(cd "$(dirname "$0")" && pwd)

for target in "$@"; do
	(cd "$root/$target" && haloy "$command" -c ../haloy.yaml -t "$target")
done
