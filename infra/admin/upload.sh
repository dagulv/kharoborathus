#!/usr/bin/env bash
# Mirrors a static build into a Bunny Storage zone, then purges its pull zone: infra/admin/upload.sh <build dir>
# Run through infra/secrets.sh prod:bunny, which sets the BUNNY_* keys.
#
# Bunny APIs, from docs.bunny.net:
#   Upload: PUT    https://$BUNNY_STORAGE_HOST/$zone/<path>, header AccessKey: <storage zone password>, raw body, optional Checksum: <uppercase hex sha256>. 201 on success.
#   List:   GET    https://$BUNNY_STORAGE_HOST/$zone/<dir>/, same AccessKey. JSON array of {ObjectName, Path, IsDirectory}.
#   Delete: DELETE https://$BUNNY_STORAGE_HOST/$zone/<path>, same AccessKey.
#   Purge:  POST   https://api.bunny.net/pullzone/<id>/purgeCache, header AccessKey: <account API key>.
set -euo pipefail

build=${1:?usage: $0 <build dir>}
: "${BUNNY_STORAGE_ZONE:?}" "${BUNNY_STORAGE_HOST:?}" "${BUNNY_STORAGE_PASSWORD:?}" "${BUNNY_API_KEY:?}" "${BUNNY_PULL_ZONE_ID:?}"

base="https://$BUNNY_STORAGE_HOST/$BUNNY_STORAGE_ZONE"

storage() {
	curl -fsS -H "AccessKey: $BUNNY_STORAGE_PASSWORD" "$@"
}

cd "$build"
mapfile -t local_files < <(find . -type f | sed 's#^\./##' | sort)

for f in "${local_files[@]}"; do
	sum=$(sha256sum "$f" | cut -d' ' -f1 | tr 'a-f' 'A-F')
	storage -X PUT -H 'Content-Type: application/octet-stream' -H "Checksum: $sum" --data-binary "@$f" "$base/$f" >/dev/null
	echo "uploaded $f"
done

# Prints every stored file under a directory, relative to the zone root.
list() {
	storage "$base/$1" | jq -r --arg zone "/$BUNNY_STORAGE_ZONE/" '.[] | (.Path | ltrimstr($zone)) + .ObjectName + (if .IsDirectory then "/" else "" end)' |
		while IFS= read -r entry; do
			if [[ $entry == */ ]]; then
				list "$entry"
			else
				echo "$entry"
			fi
		done
}

# A lookup table, not grep -q over a pipe: under pipefail, grep closing the pipe early made a kept file look stale.
declare -A keep
for f in "${local_files[@]}"; do
	keep[$f]=1
done

# Listed into a file first, since set -e does not see a failure inside a process substitution.
stored_files=$(mktemp)
trap 'rm -f "$stored_files"' EXIT
list "" >"$stored_files"

# Files from older builds, such as hashed bundles, would otherwise pile up.
while IFS= read -r stored; do
	if [[ ! -v keep[$stored] ]]; then
		storage -X DELETE "$base/$stored" >/dev/null
		echo "deleted $stored"
	fi
done <"$stored_files"

curl -fsS -X POST -H "AccessKey: $BUNNY_API_KEY" "https://api.bunny.net/pullzone/$BUNNY_PULL_ZONE_ID/purgeCache" >/dev/null
echo "purged pull zone $BUNNY_PULL_ZONE_ID"
