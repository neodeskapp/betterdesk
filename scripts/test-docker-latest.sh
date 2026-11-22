#!/usr/bin/env bash
# Checks of scripts/docker-latest.sh, which decides whether the Docker
# images of a v* tag take the tag latest. curl is a stand-in that answers
# GitHub's release routes from files of a temporary folder.
#
#   bash scripts/test-docker-latest.sh
set -euo pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
export TEST_DIR
trap 'rm -rf "$TEST_DIR"' EXIT
mkdir -p "$TEST_DIR/bin"

# curl: answers $GITHUB_API_URL/repos/o/r/releases/latest with
# $TEST_DIR/latest.json (404 when it is missing) and the release list with
# $TEST_DIR/list.json; $TEST_DIR/status overrides every status. Each URL
# goes to $TEST_DIR/calls, each Authorization header to $TEST_DIR/auth.
cat > "$TEST_DIR/bin/curl" <<'EOF'
#!/usr/bin/env bash
out="" url="" prev=""
for arg in "$@"; do
    [ "$prev" = -o ] && out="$arg"
    [ "$prev" = -H ] && case "$arg" in Authorization:*) printf '%s\n' "$arg" >> "$TEST_DIR/auth" ;; esac
    case "$arg" in http*) url="$arg" ;; esac
    prev="$arg"
done
printf '%s\n' "$url" >> "$TEST_DIR/calls"
case "$url" in
    */releases/latest) file="$TEST_DIR/latest.json" ;;
    */releases\?*) file="$TEST_DIR/list.json" ;;
    *) file="" ;;
esac
if [ -f "$TEST_DIR/status" ]; then
    code=$(cat "$TEST_DIR/status")
    printf '{"message":"Server Error"}' > "$out"
elif [ -n "$file" ] && [ -f "$file" ]; then
    code=200
    cp "$file" "$out"
else
    code=404
    printf '{"message":"Not Found"}' > "$out"
fi
printf '%s' "$code"
EOF
chmod +x "$TEST_DIR/bin/curl"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

# reset: no stable release, no release list, no calls recorded
reset() {
    rm -f "$TEST_DIR/latest.json" "$TEST_DIR/list.json" "$TEST_DIR/status" "$TEST_DIR/auth"
    : > "$TEST_DIR/calls"
}

# releases TAG:prerelease:draft ...: the release list GitHub answers
releases() {
    local entry tag pre draft sep=""
    {
        printf '['
        for entry in "$@"; do
            IFS=: read -r tag pre draft <<<"$entry"
            printf '%s{"tag_name":"%s","prerelease":%s,"draft":%s}' "$sep" "$tag" "$pre" "$draft"
            sep=,
        done
        printf ']'
    } > "$TEST_DIR/list.json"
}

# decide TAG [TOKEN]: what the script prints for TAG, run with TOKEN in
# GH_TOKEN; its exit status in $status
decide() {
    status=0
    out=$(PATH="$TEST_DIR/bin:$PATH" GITHUB_REPOSITORY=o/r GH_TOKEN="${2:-}" \
        GITHUB_API_URL=https://api.test bash "$SCRIPT_ROOT/scripts/docker-latest.sh" "$1" 2>"$TEST_DIR/stderr") || status=$?
}

# expect OUT: the last decide exited 0 and printed OUT
expect() {
    [ "$status" = 0 ] && [ "$out" = "$1" ]
}

# A stable release always takes latest, without asking GitHub
reset
decide v3.6.0
expect true || fail "v3.6.0: '$out' (exit $status), want true"
[ ! -s "$TEST_DIR/calls" ] || fail "v3.6.0 asked GitHub: $(cat "$TEST_DIR/calls")"

# The first release candidate, with only the rolling dev pre-release out
reset
releases dev:true:false
decide v3.6.0-rc.1
expect true || fail "first rc: '$out' (exit $status), want true; $(cat "$TEST_DIR/stderr")"
grep -qx 'https://api.test/repos/o/r/releases/latest' "$TEST_DIR/calls" || fail "first rc did not look for a stable release: $(cat "$TEST_DIR/calls")"

# The release of the tag itself may already be published
reset
releases v3.6.0-rc.1:true:false dev:true:false
decide v3.6.0-rc.1
[ "$out" = true ] || fail "rc with its own release: '$out', want true"

# A newer published pre-release keeps latest (also when the list is not
# in version order); a newer draft does not count
reset
releases v3.6.0-rc.2:true:false dev:true:false v3.6.0-rc.1:true:false
decide v3.6.0-rc.1
expect false || fail "older rc: '$out' (exit $status), want false"
reset
releases v3.6.0-rc.9:true:false v3.6.0-rc.10:true:false
decide v3.6.0-rc.10
[ "$out" = true ] || fail "rc.10 after rc.9: '$out', want true"
reset
releases v3.7.0-rc.1:true:true v3.6.0-rc.1:true:false
decide v3.6.0-rc.2
[ "$out" = true ] || fail "a newer draft kept latest: '$out', want true"

# Once a stable release exists, a release candidate leaves latest alone
reset
printf '{"tag_name":"v3.6.0","prerelease":false,"draft":false}' > "$TEST_DIR/latest.json"
releases v3.6.0:false:false
decide v3.7.0-rc.1
expect false || fail "rc after a stable release: '$out' (exit $status), want false"

# GitHub not answering is an error, not a guess
reset
printf '503' > "$TEST_DIR/status"
decide v3.6.0-rc.1
[ "$status" != 0 ] || fail "a 503 gave '$out' with exit 0"
[ -z "$out" ] || fail "a 503 printed '$out'"

# Only v* tags are release tags
reset
decide dev
[ "$status" != 0 ] || fail "the tag dev gave '$out' with exit 0"

# The workflow's token goes along when it is set
reset
releases dev:true:false
decide v3.6.0-rc.1 test-token
[ "$out" = true ] || fail "with a token: '$out', want true"
grep -qx 'Authorization: Bearer test-token' "$TEST_DIR/auth" 2>/dev/null || fail "the token was not sent"

echo "docker-latest: all checks passed"
