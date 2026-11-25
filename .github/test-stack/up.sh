#!/usr/bin/env bash
# Start a seeded NeoDesk test instance on a CI runner, for the contract
# suite and the e2e tests. The instance is a container named ndgo-<name>
# on the docker network ndgo, with the checkout at /repo and the server
# and devseed from <bin dir>; instance.sh runs inside it.
#
#   up.sh start <name> <bin dir>   start it and return; STACK_SEED=0 in the
#                                  environment leaves it without the seed, and
#                                  SESSION_MAX_PER_USER (also empty) replaces
#                                  the instance's limit of one session
#   up.sh wait <name>              wait until it is seeded, then make
#                                  http://ndgo-<name>:5000 reachable from the
#                                  runner and its secrets readable
#   up.sh logs <name>              print its logs
#
# The secrets are in $RUNNER_TEMP/ndgo-<name>/secrets (e2e_password, the
# device tokens), the logs in $RUNNER_TEMP/ndgo-<name>/logs.
set -euo pipefail
cmd="${1:?start, wait or logs}"
name="${2:?instance name}"
dir="$RUNNER_TEMP/ndgo-$name"

case "$cmd" in
    start)
        bin="${3:?bin dir}"
        mkdir -p "$dir/secrets" "$dir/logs"
        chmod +x "$bin/neodesk-server" "$bin/devseed"
        docker network inspect ndgo >/dev/null 2>&1 || docker network create ndgo >/dev/null
        # The console keeps its state in /repo/web-nodejs, a fresh tmpfs here
        # as in the local stack. The image is the base of the local stack's
        # (Debian with Node): the server terminal's answers name its first
        # account, node.
        # UPDATE_GITHUB_TOKEN, when the step sets it, goes into the instance
        # by name: the update check then asks GitHub with the job's token
        # instead of the runner address's 60 requests an hour
        docker run -d --name "ndgo-$name" --hostname "ndgo-$name" --network ndgo \
            ${UPDATE_GITHUB_TOKEN:+-e UPDATE_GITHUB_TOKEN} ${STACK_SEED:+-e STACK_SEED} ${SESSION_MAX_PER_USER+-e SESSION_MAX_PER_USER} \
            -v "$GITHUB_WORKSPACE:/repo" --tmpfs /repo/web-nodejs:exec,mode=0755 \
            -v "$dir/secrets:/stack/secrets" -v "$dir/logs:/stack/logs" \
            -v "$bin/neodesk-server:/usr/local/bin/neodesk-server:ro" \
            -v "$bin/devseed:/usr/local/bin/devseed:ro" \
            node:22-bookworm-slim bash /repo/.github/test-stack/instance.sh >/dev/null
        echo "ndgo-$name started"
        ;;
    wait)
        for _ in $(seq 1 300); do
            if docker exec "ndgo-$name" test -f /stack/ready 2>/dev/null; then
                ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "ndgo-$name")"
                echo "$ip ndgo-$name" | sudo tee -a /etc/hosts >/dev/null
                sudo chmod -R a+rX "$dir/secrets" "$dir/logs"
                echo "ndgo-$name is ready at http://ndgo-$name:5000"
                exit 0
            fi
            if [ "$(docker inspect -f '{{.State.Running}}' "ndgo-$name" 2>/dev/null)" != "true" ]; then
                echo "ndgo-$name stopped during start:"
                docker logs --tail 80 "ndgo-$name"
                exit 1
            fi
            sleep 2
        done
        echo "ndgo-$name did not become ready:"
        docker logs --tail 80 "ndgo-$name"
        exit 1
        ;;
    logs)
        docker logs --tail 80 "ndgo-$name" || true
        sudo chmod -R a+rX "$dir/logs" 2>/dev/null || true
        # Lines that could carry a password or token of the instance stay out
        for f in "$dir"/logs/*.log; do
            [ -f "$f" ] || continue
            echo "== $(basename "$f")"
            tail -n 200 "$f" | grep -v -i -E 'password|secret|token' || true
        done
        ;;
    *)
        sed -n '2,15p' "$0"
        exit 2
        ;;
esac
