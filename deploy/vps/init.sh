#!/bin/sh
set -eu

connectivity_domain=""
turn_domain=""
acme_email=""
vps_public_ip=""
turn_relay_ip=""
computehop_version="dev"
target_dir=""
force=false

usage() {
	echo "Usage: deploy/vps/init.sh --connectivity-domain DOMAIN --turn-domain DOMAIN" >&2
	echo "       --email EMAIL --public-ip IPv4 [--relay-ip IPv4] [--version VERSION]" >&2
	echo "       [--target-dir DIR] [--force]" >&2
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--connectivity-domain|--connect-domain)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			connectivity_domain=$2
			shift 2
			;;
		--turn-domain|--turn-realm)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			turn_domain=$2
			shift 2
			;;
		--email|--acme-email)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			acme_email=$2
			shift 2
			;;
		--public-ip|--vps-public-ip)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			vps_public_ip=$2
			shift 2
			;;
		--relay-ip|--turn-relay-ip)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			turn_relay_ip=$2
			shift 2
			;;
		--version)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			computehop_version=$2
			shift 2
			;;
		--target-dir)
			[ "$#" -ge 2 ] || { usage; exit 1; }
			target_dir=$2
			shift 2
			;;
		--force)
			force=true
			shift
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

validate_domain() {
	label=$1
	value=$2
	case "$value" in
		""|.*|*..*|*.|*[!A-Za-z0-9.-]*)
			echo "$label must be a DNS name containing only letters, numbers, dots, and dashes." >&2
			exit 1
			;;
	esac
}

validate_ipv4() {
	label=$1
	value=$2
	case "$value" in
		""|*[!0-9.]*|*.*.*.*.*)
			echo "$label must be an IPv4 address." >&2
			exit 1
			;;
	esac
}

validate_domain "--connectivity-domain" "$connectivity_domain"
validate_domain "--turn-domain" "$turn_domain"
validate_ipv4 "--public-ip" "$vps_public_ip"
if [ -n "$turn_relay_ip" ]; then
	validate_ipv4 "--relay-ip" "$turn_relay_ip"
fi
case "$acme_email" in
	*@*.*) ;;
	*) echo "--email must look like an operations email address." >&2; exit 1 ;;
esac
case "$computehop_version" in
	""|*[!A-Za-z0-9._-]*)
		echo "--version contains unsupported characters." >&2
		exit 1
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
if [ -z "$target_dir" ]; then
	target_dir=$script_dir
else
	case "$target_dir" in
		/*) ;;
		*) target_dir=$(pwd)/$target_dir ;;
	esac
fi
secret_dir="$target_dir/secrets"
env_file="$target_dir/.env"
secret_file="$secret_dir/turn_shared_secret"
temporary_env=""
temporary_secret=""

cleanup() {
	if [ -n "$temporary_env" ] && [ -f "$temporary_env" ]; then
		rm -f -- "$temporary_env"
	fi
	if [ -n "$temporary_secret" ] && [ -f "$temporary_secret" ]; then
		rm -f -- "$temporary_secret"
	fi
}
trap cleanup EXIT HUP INT TERM

if [ -e "$env_file" ] && [ ! -f "$env_file" ]; then
	echo "Refusing to replace non-file $env_file" >&2
	exit 1
fi
if [ -f "$env_file" ] && [ "$force" != true ]; then
	echo "$env_file already exists; pass --force to rewrite it." >&2
	exit 1
fi
if [ -e "$secret_file" ] && [ ! -f "$secret_file" ]; then
	echo "Refusing to replace non-file $secret_file" >&2
	exit 1
fi
if ! command -v openssl >/dev/null 2>&1; then
	echo "Required tool is missing: openssl" >&2
	exit 1
fi

mkdir -p "$target_dir" "$secret_dir"
chmod 0700 "$secret_dir"

temporary_env=$(mktemp "$target_dir/.env.XXXXXX")
cat > "$temporary_env" <<EOF
CONNECTIVITY_DOMAIN=$connectivity_domain
ACME_EMAIL=$acme_email
TURN_REALM=$turn_domain
VPS_PUBLIC_IP=$vps_public_ip
TURN_RELAY_IP=${turn_relay_ip:-$vps_public_ip}
COMPUTEHOP_VERSION=$computehop_version
EOF
chmod 0600 "$temporary_env"
mv "$temporary_env" "$env_file"
temporary_env=""

created_secret=false
if [ ! -s "$secret_file" ]; then
	temporary_secret=$(mktemp "$secret_dir/turn_shared_secret.XXXXXX")
	openssl rand -hex 32 > "$temporary_secret"
	chmod 0600 "$temporary_secret"
	mv "$temporary_secret" "$secret_file"
	temporary_secret=""
	created_secret=true
else
	chmod 0600 "$secret_file"
fi

echo "Wrote $env_file"
if [ "$created_secret" = true ]; then
	echo "Created $secret_file"
else
	echo "Kept existing $secret_file"
fi
cat <<EOF

Next:
- Review DNS and provider firewall rules.
- Run: cd $target_dir && docker compose config --quiet
- Run: cd $target_dir && docker compose up -d --build
- Verify: cd $target_dir && ./verify.sh
EOF
