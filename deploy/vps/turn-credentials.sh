#!/bin/sh
set -eu

env_file="./.env"
secret_file="./secrets/turn_shared_secret"
connectivity_domain=""
turn_domain=""
ttl_hours=24
label="computehop"

usage() {
	echo "Usage: deploy/vps/turn-credentials.sh [--env-file FILE] [--secret-file FILE]" >&2
	echo "       [--connectivity-domain DOMAIN] [--turn-domain DOMAIN]" >&2
	echo "       [--ttl-hours HOURS] [--label LABEL]" >&2
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--env-file)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			env_file=$2
			shift 2
			;;
		--secret-file)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			secret_file=$2
			shift 2
			;;
		--connectivity-domain)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			connectivity_domain=$2
			shift 2
			;;
		--turn-domain|--turn-realm)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			turn_domain=$2
			shift 2
			;;
		--ttl-hours)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			ttl_hours=$2
			shift 2
			;;
		--label)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			label=$2
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			usage
			exit 1
			;;
	esac
done

if [ -f "$env_file" ]; then
	set -a
	# shellcheck disable=SC1090
	. "$env_file"
	set +a
fi

connectivity_domain=${connectivity_domain:-${CONNECTIVITY_DOMAIN:-}}
turn_domain=${turn_domain:-${TURN_REALM:-}}

validate_domain() {
	name=$1
	value=$2
	case "$value" in
		""|.*|*..*|*.|*[!A-Za-z0-9.-]*)
			echo "$name must be a DNS name containing only letters, numbers, dots, and dashes." >&2
			exit 1
			;;
	esac
}

validate_domain "--connectivity-domain" "$connectivity_domain"
validate_domain "--turn-domain" "$turn_domain"
case "$ttl_hours" in
	""|*[!0-9]*)
		echo "--ttl-hours must be a positive whole number." >&2
		exit 1
		;;
esac
if [ "$ttl_hours" -lt 1 ] || [ "$ttl_hours" -gt 720 ]; then
	echo "--ttl-hours must be between 1 and 720." >&2
	exit 1
fi
case "$label" in
	""|*[!A-Za-z0-9._-]*)
		echo "--label may contain only letters, numbers, dots, underscores, and dashes." >&2
		exit 1
		;;
esac
if [ ! -s "$secret_file" ]; then
	echo "$secret_file is missing or empty. Run deploy/vps/init.sh first." >&2
	exit 1
fi
if ! command -v openssl >/dev/null 2>&1; then
	echo "Required tool is missing: openssl" >&2
	exit 1
fi

turn_secret=$(tr -d '\r\n' < "$secret_file")
case "$turn_secret" in
	""|*[!A-Fa-f0-9]*)
		echo "$secret_file must contain the hexadecimal coturn shared secret created by init.sh." >&2
		exit 1
		;;
esac

now=$(date +%s)
expires=$((now + ttl_hours * 3600))
username="${expires}:${label}"
password=$(printf '%s' "$username" | openssl dgst -sha1 -hmac "$turn_secret" -binary | openssl base64 -A)

format_epoch() {
	if date -u -r "$1" '+%Y-%m-%dT%H:%M:%SZ' >/dev/null 2>&1; then
		date -u -r "$1" '+%Y-%m-%dT%H:%M:%SZ'
	else
		date -u -d "@$1" '+%Y-%m-%dT%H:%M:%SZ'
	fi
}

shell_arg() {
	value=$1
	case "$value" in
		""|*[!A-Za-z0-9._~:/=@%+,-]*)
			printf "'%s'" "$(printf '%s' "$value" | sed "s/'/'\\\\''/g")"
			;;
		*)
			printf '%s' "$value"
			;;
	esac
}

connectivity_url="https://$connectivity_domain"
stun_server="stun:$turn_domain:3478"
turn_server="turn:$turn_domain:3478?transport=udp"
expires_at=$(format_epoch "$expires")

cat <<EOF
ComputeHop TURN credentials

Expires:  $expires_at
Username: $username
Password: $password

Friendly setup helper commands for each Mac after pairing once on the LAN:
   computehop setup orchestrator --connectivity-domain $(shell_arg "$connectivity_domain") --turn-domain $(shell_arg "$turn_domain") --turn-server $(shell_arg "$turn_server") --turn-username $(shell_arg "$username") --turn-password $(shell_arg "$password")
   computehop setup worker --device-name "Gaming PC" --connectivity-domain $(shell_arg "$connectivity_domain") --turn-domain $(shell_arg "$turn_domain") --turn-server $(shell_arg "$turn_server") --turn-username $(shell_arg "$username") --turn-password $(shell_arg "$password")

Direct installer commands:
   ./packaging/macos/install.sh --role orchestrator --connectivity-url $(shell_arg "$connectivity_url") --stun-server $(shell_arg "$stun_server") --turn-server $(shell_arg "$turn_server") --turn-username $(shell_arg "$username") --turn-password $(shell_arg "$password")
   ./packaging/macos/install.sh --role worker --device-name "Gaming PC" --connectivity-url $(shell_arg "$connectivity_url") --stun-server $(shell_arg "$stun_server") --turn-server $(shell_arg "$turn_server") --turn-username $(shell_arg "$username") --turn-password $(shell_arg "$password")

These credentials are generated from the server-only coturn shared secret. Do not copy secrets/turn_shared_secret to client machines.
EOF
