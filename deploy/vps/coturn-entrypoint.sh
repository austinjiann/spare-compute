#!/bin/sh
set -eu

secret_file=/run/secrets/turn_shared_secret
config_file=/tmp/turnserver.conf

if [ ! -s "$secret_file" ]; then
	echo "TURN shared secret is missing" >&2
	exit 1
fi
if [ -z "${TURN_REALM:-}" ] || [ -z "${VPS_PUBLIC_IP:-}" ]; then
	echo "TURN_REALM and VPS_PUBLIC_IP are required" >&2
	exit 1
fi
relay_ip=${TURN_RELAY_IP:-$VPS_PUBLIC_IP}
case "$TURN_REALM" in
	*[!A-Za-z0-9.-]*) echo "TURN_REALM contains unsupported characters" >&2; exit 1 ;;
esac
case "$VPS_PUBLIC_IP" in
	*[!0-9.]*) echo "VPS_PUBLIC_IP must be an IPv4 address" >&2; exit 1 ;;
esac
case "$relay_ip" in
	*[!0-9.]*) echo "TURN_RELAY_IP must be an IPv4 address" >&2; exit 1 ;;
esac

turn_secret=$(tr -d '\r\n' < "$secret_file")
case "$turn_secret" in
	''|*[!A-Fa-f0-9]*) echo "TURN shared secret must be non-empty hexadecimal" >&2; exit 1 ;;
esac

umask 077
external_ip=$VPS_PUBLIC_IP
if [ "$relay_ip" != "$VPS_PUBLIC_IP" ]; then
	external_ip=$VPS_PUBLIC_IP/$relay_ip
fi
{
	printf 'listening-port=3478\n'
	printf 'relay-ip=%s\n' "$relay_ip"
	printf 'external-ip=%s\n' "$external_ip"
	printf 'realm=%s\n' "$TURN_REALM"
	printf 'static-auth-secret=%s\n' "$turn_secret"
	printf '%s\n' \
		'fingerprint' \
		'use-auth-secret' \
		'stale-nonce=600' \
		'no-cli' \
		'no-tls' \
		'no-dtls' \
		'no-tcp-relay' \
		'no-loopback-peers' \
		'no-multicast-peers' \
		'denied-peer-ip=10.0.0.0-10.255.255.255' \
		'denied-peer-ip=169.254.0.0-169.254.255.255' \
		'denied-peer-ip=172.16.0.0-172.31.255.255' \
		'denied-peer-ip=192.168.0.0-192.168.255.255' \
		'user-quota=12' \
		'total-quota=120' \
		'min-port=49160' \
		'max-port=49200' \
		'pidfile=/tmp/turnserver.pid' \
		'simple-log' \
		'no-stdout-log' \
		'log-file=stdout'
} > "$config_file"
unset turn_secret
unset external_ip relay_ip

exec turnserver -c "$config_file"
