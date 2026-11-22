#!/usr/bin/env bash
# Prints true when the Docker images of the v* tag TAG take the tag
# latest, false when latest stays where it is.
#
# A stable release (v3.6.0) always takes latest. A pre-release (a tag with
# a suffix, v3.6.0-rc.1) takes it while the repository has no stable
# release and no published pre-release other than dev with a newer
# version: the release the installers' stable channel installs then.
#
#   GITHUB_REPOSITORY=owner/repo GH_TOKEN=... scripts/docker-latest.sh v3.6.0-rc.1
#
# GH_TOKEN is optional; GITHUB_API_URL is GitHub's API unless set. Exits
# with an error, printing nothing, when GitHub does not answer.
set -euo pipefail

tag="${1:-}"
case "$tag" in
    v*-*) ;;
    v*) echo true; exit 0 ;;
    *) echo "docker-latest: '$tag' is not a v* tag" >&2; exit 2 ;;
esac
api="${GITHUB_API_URL:-https://api.github.com}/repos/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is not set}"
headers=(-H 'Accept: application/vnd.github+json')
[ -n "${GH_TOKEN:-}" ] && headers+=(-H "Authorization: Bearer $GH_TOKEN")
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# get PATH FILE: GitHub's answer to PATH into FILE; prints the HTTP status
get() {
    curl -sS --retry 3 --connect-timeout 20 "${headers[@]}" -o "$2" -w '%{http_code}' "$api$1" || true
}

code="$(get /releases/latest "$tmp/latest")"
case "$code" in
    200)
        echo "docker-latest: the stable release $(jq -r .tag_name "$tmp/latest") keeps latest" >&2
        echo false
        exit 0
        ;;
    404) ;;
    *) echo "docker-latest: GitHub answered ${code:-nothing} for the latest release" >&2; exit 1 ;;
esac

code="$(get '/releases?per_page=100' "$tmp/list")"
[ "$code" = 200 ] || { echo "docker-latest: GitHub answered ${code:-nothing} for the release list" >&2; exit 1; }
newest="$( { jq -r '.[] | select(.draft == false and .prerelease == true and .tag_name != "dev") | .tag_name' "$tmp/list"
    printf '%s\n' "$tag"; } | grep '^v' | sort -V | tail -n 1)"
if [ "$newest" = "$tag" ]; then
    echo "docker-latest: no stable release yet, and $tag is the newest pre-release" >&2
    echo true
else
    echo "docker-latest: no stable release yet, but the pre-release $newest is newer than $tag" >&2
    echo false
fi
