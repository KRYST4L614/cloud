#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

compose() {
  docker compose --env-file deployments/gitlab/.env -f deployments/gitlab/compose.yml "$@"
}

if [ ! -f deployments/gitlab/.env ]; then
  cp deployments/gitlab/.env.example deployments/gitlab/.env
fi
git rev-parse --verify HEAD >/dev/null
compose up -d

echo 'Waiting for GitLab (first startup can take several minutes)...'
ready=0
for attempt in $(seq 1 120); do
  if compose exec -T gitlab curl --silent --fail --max-time 5 \
    http://127.0.0.1:9080/users/sign_in >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 5
done
if [ "$ready" != 1 ]; then
  echo 'GitLab did not become ready. Check Docker Compose logs.' >&2
  exit 1
fi

umask 077
bootstrap_dir=$(mktemp -d)
pat=
cleanup() {
  if [ -n "$pat" ]; then
    if ! printf 'PRIVATE-TOKEN: %s\n' "$pat" | compose exec -T gitlab curl \
      --silent --show-error --fail --max-time 15 --header @- --request DELETE \
      http://127.0.0.1:9080/api/v4/personal_access_tokens/self >/dev/null; then
      echo 'Could not revoke temporary GitLab token; it expires tomorrow.' >&2
    fi
  fi
  rm -rf "$bootstrap_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo 'Configuring project root/cloud and Docker Runner...'
compose exec -T gitlab gitlab-rails runner - < scripts/setup-local-ci.rb > "$bootstrap_dir/result"
pat=$(sed -n 's/^CLOUD_PAT=//p' "$bootstrap_dir/result")
CI_SERVER_TOKEN=$(sed -n 's/^CLOUD_RUNNER_TOKEN=//p' "$bootstrap_dir/result")
if [ -z "$pat" ] || [ -z "$CI_SERVER_TOKEN" ]; then
  echo 'GitLab bootstrap did not return the required credentials.' >&2
  exit 1
fi
export CI_SERVER_TOKEN

if ! compose exec -T runner gitlab-runner list 2>&1 | grep -q '^local-cloud-docker '; then
  compose run --rm -T -e CI_SERVER_TOKEN runner register --non-interactive \
    --url http://gitlab.lan:9080 --name local-cloud-docker --executor docker \
    --docker-image docker:29.9.3-cli --docker-network-mode cloud-ci \
    --docker-volumes /var/run/docker.sock:/var/run/docker.sock --docker-volumes /cache
fi
unset CI_SERVER_TOKEN
compose exec -T runner gitlab-runner verify

# No token in the remote URL, Git config, credential store, or process arguments.
cat > "$bootstrap_dir/askpass" <<'ASKPASS'
#!/bin/sh
case "$1" in
  *Username*) printf '%s\n' oauth2 ;;
  *) printf '%s\n' "$CLOUD_BOOTSTRAP_PAT" ;;
esac
ASKPASS
chmod 700 "$bootstrap_dir/askpass"
address=$(compose port gitlab 9080)
case "$address" in
  0.0.0.0:*) address="127.0.0.1:${address##*:}" ;;
esac

if [ -n "$(git status --porcelain)" ]; then
  echo 'Uncommitted changes stay local; importing the current commit only.'
fi
echo 'Importing HEAD into root/cloud:main (without tags)...'
CLOUD_BOOTSTRAP_PAT="$pat" GIT_ASKPASS="$bootstrap_dir/askpass" GIT_TERMINAL_PROMPT=0 \
  git -c credential.helper= -c http.proxy= push --no-follow-tags \
    "http://oauth2@$address/root/cloud.git" HEAD:refs/heads/main

echo "GitLab project ready: http://$address/root/cloud"
