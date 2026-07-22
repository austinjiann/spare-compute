#!/bin/sh
set -eu

if [ ! -f .env ]; then
	echo "deploy/vps/.env is missing; copy .env.example and fill it in" >&2
	exit 1
fi
if [ ! -s secrets/turn_shared_secret ]; then
	echo "deploy/vps/secrets/turn_shared_secret is missing" >&2
	exit 1
fi

set -a
# shellcheck disable=SC1091
. ./.env
set +a

docker compose config --quiet
docker compose ps
curl --fail --show-error --silent "https://${CONNECTIVITY_DOMAIN}/healthz"
printf '\nHTTPS rendezvous is healthy.\n'

docker compose exec -T coturn turnutils_stunclient -p 3478 127.0.0.1
docker compose exec -T coturn /bin/sh -c \
  'exec turnutils_uclient -p 3478 -W "$(tr -d "\r\n" < /run/secrets/turn_shared_secret)" -v -y 127.0.0.1'
printf 'Local STUN and authenticated TURN allocation passed.\n'
printf 'Test from another network and force a relayed ComputeHop session before launch.\n'
