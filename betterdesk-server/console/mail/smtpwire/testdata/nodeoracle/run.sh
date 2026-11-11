#!/usr/bin/env bash
# Records the fixtures of the smtpwire tests with the real nodemailer.
# Runs inside the dev image (see README.md):
#   console/mail/smtpwire/testdata/nodeoracle/run.sh [sessions|vectors|e2e]...
set -euo pipefail
cd "$(dirname "$0")"
# the test certificates the client is to trust, nodemailer has no option for
# them on the console's path
export NODE_EXTRA_CA_CERTS="$PWD/../certs/trusted.pem"
export WEB_NODEJS="${WEB_NODEJS:-/repo/web-nodejs}"
what=("$@")
if [ ${#what[@]} -eq 0 ]; then what=(sessions vectors e2e); fi
for part in "${what[@]}"; do
    node "record_${part}.js"
done
