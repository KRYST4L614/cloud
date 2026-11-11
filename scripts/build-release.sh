#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

: "${GITHUB_SHA:?GITHUB_SHA is required}"
platform=${BUILD_PLATFORM:-linux/amd64}
backend_image="cloud-backend:$GITHUB_SHA"
frontend_image="cloud-frontend:$GITHUB_SHA"

docker build --platform "$platform" -f deployments/Dockerfile -t "$backend_image" .
docker build --platform "$platform" -f web/Dockerfile -t "$frontend_image" web

release_dir=$(mktemp -d)
trap 'rm -rf "$release_dir"' EXIT
mkdir -p "$release_dir/deployments" "$release_dir/scripts"
cp docker-compose.yml "$release_dir/"
cp deployments/docker-compose.release.yml "$release_dir/deployments/"
cp scripts/deploy-release.sh "$release_dir/scripts/"
cp -R configs "$release_dir/"
printf 'BACKEND_IMAGE=%s\nFRONTEND_IMAGE=%s\n' \
  "$backend_image" "$frontend_image" > "$release_dir/release.env"
docker save -o "$release_dir/images.tar" "$backend_image" "$frontend_image"
tar -czf release.tgz -C "$release_dir" .
