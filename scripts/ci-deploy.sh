#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

: "${DEPLOY_HOST:?Set DEPLOY_HOST in GitHub Actions secrets}"
: "${DEPLOY_USER:?Set DEPLOY_USER to your existing VPS user}"
: "${SSH_PRIVATE_KEY:?Set SSH_PRIVATE_KEY in GitHub Actions secrets}"
: "${SSH_KNOWN_HOSTS:?Set SSH_KNOWN_HOSTS in GitHub Actions secrets}"
: "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}"
run_attempt=${GITHUB_RUN_ATTEMPT:-1}
case "$GITHUB_RUN_ID" in
  ''|*[!0-9]*) echo 'GITHUB_RUN_ID must be numeric' >&2; exit 1 ;;
esac
case "$run_attempt" in
  ''|*[!0-9]*) echo 'GITHUB_RUN_ATTEMPT must be numeric' >&2; exit 1 ;;
esac
release_id="$GITHUB_RUN_ID-$run_attempt"
deploy_port=${DEPLOY_PORT:-22}

umask 077
ssh_dir=$(mktemp -d)
trap 'rm -rf "$ssh_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf '%s\n' "$SSH_PRIVATE_KEY" | tr -d '\r' > "$ssh_dir/key"
printf '%s\n' "$SSH_KNOWN_HOSTS" | tr -d '\r' > "$ssh_dir/known_hosts"
chmod 600 "$ssh_dir/key" "$ssh_dir/known_hosts"

# The runner sends a complete release; the VPS only needs Docker and SSH.
# A fresh directory avoids retaining files deleted in a newer Git revision.
ssh -p "$deploy_port" -i "$ssh_dir/key" \
  -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=15 \
  -o "UserKnownHostsFile=$ssh_dir/known_hosts" \
  "$DEPLOY_USER@$DEPLOY_HOST" \
  "set -eu
   mkdir -p \"\$HOME/cloud/releases/$release_id\"
   tar -xzf - -C \"\$HOME/cloud/releases/$release_id\"
   bash \"\$HOME/cloud/releases/$release_id/scripts/deploy-release.sh\"" \
  < release.tgz
