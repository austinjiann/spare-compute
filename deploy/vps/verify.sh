#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
cd "$script_dir"

fail() {
	echo "$1" >&2
	exit 1
}

if [ ! -f .env ]; then
	fail "deploy/vps/.env is missing. Create it with: ./init.sh --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10"
fi
if [ ! -s secrets/turn_shared_secret ]; then
	fail "deploy/vps/secrets/turn_shared_secret is missing. Run ./init.sh before verifying the stack."
fi
if ! command -v docker >/dev/null 2>&1; then
	fail "Docker is not installed. On a fresh Ubuntu VPS, run: sudo ./bootstrap-ubuntu.sh"
fi
if ! docker compose version >/dev/null 2>&1; then
	fail "Docker Compose is not available. On a fresh Ubuntu VPS, run: sudo ./bootstrap-ubuntu.sh"
fi

set -a
# shellcheck disable=SC1091
. ./.env
set +a

docker compose config --quiet || fail "Docker Compose config is invalid. Check deploy/vps/.env, compose.yaml, and Caddyfile."
docker compose ps || fail "Docker Compose cannot list the ComputeHop services. Start them with: docker compose up -d --build"
running_services=$(docker compose ps --services --status running 2>/dev/null || true)
service_running() {
	printf '%s\n' "$running_services" | grep -qx "$1"
}
for service in rendezvous caddy coturn; do
	if ! service_running "$service"; then
		fail "Docker service '$service' is not running. Start or repair the stack with: docker compose up -d --build"
	fi
done
if ! curl --fail --show-error --silent "https://${CONNECTIVITY_DOMAIN}/healthz"; then
	fail "HTTPS health check failed for https://${CONNECTIVITY_DOMAIN}/healthz. Check DNS, ports 80/443, Caddy logs, and that docker compose up -d --build has completed."
fi
printf '\nHTTPS rendezvous is healthy.\n'

docker compose exec -T coturn turnutils_stunclient -p 3478 127.0.0.1 || \
	fail "Local STUN check failed. Check that coturn is running and port 3478 is open."
docker compose exec -T coturn /bin/sh -c \
  'exec turnutils_uclient -p 3478 -W "$(tr -d "\r\n" < /run/secrets/turn_shared_secret)" -v -y 127.0.0.1' || \
	fail "Authenticated TURN allocation failed. Recreate coturn after checking secrets/turn_shared_secret and TURN_REALM."
printf 'Local STUN and authenticated TURN allocation passed.\n'
printf 'Generate client TURN credentials with ./turn-credentials.sh when you are ready to test relay fallback.\n'
printf 'Test from another network and force a relayed ComputeHop session before launch.\n'
