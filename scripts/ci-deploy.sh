#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

: "${DEPLOY_HOST:?Set DEPLOY_HOST in GitLab CI/CD variables}"
: "${DEPLOY_USER:?Set DEPLOY_USER to your existing VPS user}"
: "${SSH_PRIVATE_KEY:?Set SSH_PRIVATE_KEY as a GitLab File variable}"
: "${SSH_KNOWN_HOSTS:?Set SSH_KNOWN_HOSTS as a GitLab File variable}"
: "${CI_PIPELINE_ID:?CI_PIPELINE_ID is required}"
case "$CI_PIPELINE_ID" in
  ''|*[!0-9]*) echo 'CI_PIPELINE_ID must be numeric' >&2; exit 1 ;;
esac
deploy_port=${DEPLOY_PORT:-22}

ssh_dir=$(mktemp -d)
trap 'rm -rf "$ssh_dir"' EXIT
cp "$SSH_PRIVATE_KEY" "$ssh_dir/key"
cp "$SSH_KNOWN_HOSTS" "$ssh_dir/known_hosts"
chmod 600 "$ssh_dir/key" "$ssh_dir/known_hosts"

# Only the outbound SSH connection is needed; the VPS never contacts GitLab.
# A fresh directory avoids retaining files deleted in a newer Git revision.
ssh -p "$deploy_port" -i "$ssh_dir/key" \
  -o BatchMode=yes -o StrictHostKeyChecking=yes \
  -o "UserKnownHostsFile=$ssh_dir/known_hosts" \
  "$DEPLOY_USER@$DEPLOY_HOST" \
  "set -eu
   mkdir -p \"\$HOME/cloud/releases/$CI_PIPELINE_ID\"
   tar -xzf - -C \"\$HOME/cloud/releases/$CI_PIPELINE_ID\"
   bash \"\$HOME/cloud/releases/$CI_PIPELINE_ID/scripts/deploy-release.sh\"" \
  < release.tgz
