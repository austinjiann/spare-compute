# One-VPS staging stack

This directory runs ComputeHop's first hosted connectivity environment on one
small public Linux VPS. It is provider-neutral and works on a DigitalOcean,
Hetzner, Linode/Akamai, Vultr, or similar Ubuntu host.

The stack contains:

- Caddy on ports 80/443 for automatic HTTPS and reverse proxying.
- One private `computehop-connectivity` container for opaque rendezvous and
  signaling.
- Coturn on port 3478 plus UDP 49160-49200 for authenticated STUN/TURN.

It does not host jobs, project files, device keys, commands, or artifacts. The
rendezvous state is intentionally in memory and must stay at one replica until
the service gains a shared ephemeral store. TURN is authenticated; never remove
`use-auth-secret` or expose an anonymous relay.

## Buy and prepare the VPS

Start with Ubuntu 24.04 LTS, one shared vCPU, 1 GiB RAM, a static public IPv4
address, and at least 1 TiB monthly transfer. Put it near the initial testers.
TURN egress is the variable cost, so enable provider bandwidth alerts before
inviting users.

Create two DNS A records pointing at the VPS:

```text
connect.example.com  -> VPS public IPv4
turn.example.com     -> VPS public IPv4
```

At the provider firewall, allow inbound TCP 22 from your own IP, TCP 80 and
443, UDP 443, TCP/UDP 3478, and UDP 49160-49200. Then clone the repository on
the VPS and run the included Ubuntu bootstrap once:

```bash
sudo ./deploy/vps/bootstrap-ubuntu.sh
```

The bootstrap installs Docker from Docker's official apt repository and mirrors
those ports in UFW. Review it before running it on a non-disposable host.

## Configure and start

From `deploy/vps`:

```bash
cp .env.example .env
nano .env
umask 077
openssl rand -hex 32 > secrets/turn_shared_secret
docker compose config --quiet
docker compose up -d --build
docker compose ps
```

Use real domains, an operations email, and the VPS's public IPv4 in `.env`.
`TURN_RELAY_IP` normally equals that public address. If `ip -4 addr` does not
show the public address because the provider uses 1:1 NAT, set it to the host's
primary private IPv4; coturn will advertise the public/private mapping. Neither
`.env` nor `secrets/turn_shared_secret` is committed. Caddy obtains and renews
the HTTPS certificate after DNS and ports 80/443 are working.

Verify the deployment from the VPS:

```bash
./verify.sh
docker compose logs --tail=100 rendezvous caddy coturn
```

Then verify from a machine on another network:

```bash
curl --fail https://connect.example.com/healthz
turnutils_stunclient -p 3478 turn.example.com
```

`verify.sh` performs an authenticated TURN allocation locally without copying
the shared secret off the VPS. The production client will receive short-lived
TURN credentials rather than that shared secret. A successful health endpoint
and STUN response still do not prove the public relay path works, so a
forced-relay ComputeHop test from another network remains a launch gate.

## Update and rollback

Deploy only a tested commit from `main`:

```bash
git pull --ff-only
docker compose build --pull rendezvous
docker compose up -d
./verify.sh
```

Before an update, record `git rev-parse HEAD`. To roll back, check out that
known-good commit and run `docker compose up -d --build` again. Caddy's named
volumes preserve certificates; rendezvous presence safely repopulates after a
restart.

Rotate the TURN secret after suspected exposure by replacing the secret file
atomically and recreating coturn. Existing short-lived TURN credentials stop
working after the restart:

```bash
umask 077
openssl rand -hex 32 > secrets/turn_shared_secret.next
mv secrets/turn_shared_secret.next secrets/turn_shared_secret
docker compose up -d --force-recreate coturn
```

## Current boundary

This stack makes the public services deployable, but the daemon does not yet
publish presence to rendezvous, gather ICE candidates, or select TURN paths.
LAN execution remains the working path until that client-side connectivity
slice is implemented and physically tested.
