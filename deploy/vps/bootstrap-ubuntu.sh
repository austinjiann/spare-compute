#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
	echo "run this script as root on a fresh Ubuntu VPS" >&2
	exit 1
fi

if [ ! -r /etc/os-release ]; then
	echo "cannot identify this host because /etc/os-release is missing" >&2
	echo "use Ubuntu 24.04 LTS, or install Docker and open the documented ports manually" >&2
	exit 1
fi
. /etc/os-release
if [ "${ID:-}" != "ubuntu" ] || [ -z "${VERSION_CODENAME:-}" ]; then
	echo "this bootstrap currently supports Ubuntu VPS images only" >&2
	echo "use Ubuntu 24.04 LTS, or install Docker and open the documented ports manually" >&2
	exit 1
fi

apt-get update
apt-get install -y ca-certificates curl git ufw
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
printf 'Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: %s\nComponents: stable\nSigned-By: /etc/apt/keyrings/docker.asc\n' "$VERSION_CODENAME" > /etc/apt/sources.list.d/docker.sources
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw allow 3478/tcp
ufw allow 3478/udp
ufw allow 49160:49200/udp
ufw --force enable

systemctl enable --now docker
cat <<'EOF'
VPS prerequisites and firewall are ready.

Next:
1. Restrict SSH to your IP in the provider/cloud firewall too.
2. Point DNS at this VPS:
   connect.example.com -> this VPS public IPv4
   turn.example.com    -> this VPS public IPv4
3. From the repository root on this VPS, configure and start the stack:
   ./deploy/vps/init.sh --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10
   docker compose --project-directory deploy/vps up -d --build
   ./deploy/vps/verify.sh
4. On your Mac, run this with the real domains/IP for the full checklist:
   computehop setup vps --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10
EOF
