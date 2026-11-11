#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
release_dir=$(pwd -P)
deploy_root=${CLOUD_DEPLOY_ROOT:-$HOME/cloud}

if [[ ! -f "$deploy_root/.env" ]]; then
  echo "Create $deploy_root/.env from .env.example before deployment" >&2
  exit 1
fi

compose() {
  docker compose -p cloud-storage \
    --env-file "$deploy_root/.env" --env-file "$release_dir/release.env" \
    -f "$release_dir/docker-compose.yml" \
    -f "$release_dir/deployments/docker-compose.release.yml" "$@"
}

diagnostics() {
  compose ps || true
  compose logs --tail 100 backend frontend versitygw || true
}

compose config --quiet
docker load --input "$release_dir/images.tar"
if ! compose up -d --no-build --pull missing --wait --wait-timeout 180; then
  diagnostics
  exit 1
fi

ready=0
for ((attempt = 0; attempt < 30; attempt++)); do
  if compose exec -T backend wget -qO- http://127.0.0.1:8000/readyz >/dev/null; then
    ready=1
    break
  fi
  sleep 2
done
if [[ "$ready" != 1 ]]; then
  diagnostics
  echo 'Backend dependencies did not become ready' >&2
  exit 1
fi
compose exec -T frontend wget -qO- http://127.0.0.1/ >/dev/null
ln -sfn "$release_dir" "$deploy_root/current"
echo "Deployment successful: $release_dir"
