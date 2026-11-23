#!/usr/bin/env bash
# Prints the notes of a release: release-notes/<tag>.md, then every commit
# since the release before, folded, and the compare link.
#   bash release-notes/body.sh <tag> [<previous tag>]
# The previous tag defaults to the newest v* tag before <tag>.
set -euo pipefail
tag=$1
dir=$(dirname "$0")
prev=${2:-$(git describe --tags --abbrev=0 --match 'v[0-9]*' "${tag}^" 2>/dev/null || true)}
repo="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-neodeskapp/neodesk}"

cat "${dir}/${tag}.md"
[ -n "${prev}" ] || exit 0
printf '\n<details>\n<summary>All changes since %s</summary>\n\n' "${prev}"
git log --no-merges --format='- %s (%h)' "${prev}..${tag}"
printf '\n**Full Changelog**: %s/compare/%s...%s\n\n</details>\n' "${repo}" "${prev}" "${tag}"
