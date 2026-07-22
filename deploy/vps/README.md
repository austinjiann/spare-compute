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
./init.sh \
  --connectivity-domain connect.example.com \
  --turn-domain turn.example.com \
  --email admin@example.com \
  --public-ip 203.0.113.10
docker compose config --quiet
docker compose up -d --build
docker compose ps
```

Use real domains, an operations email, and the VPS's public IPv4 in `.env`.
`TURN_RELAY_IP` normally equals that public address. If `ip -4 addr` does not
show the public address because the provider uses 1:1 NAT, set it to the host's
primary private IPv4 with `--relay-ip`; coturn will advertise the
public/private mapping. `init.sh` refuses to overwrite an existing `.env`
unless `--force` is passed and preserves an existing TURN secret. Neither `.env`
nor `secrets/turn_shared_secret` is committed. Caddy obtains and renews the
HTTPS certificate after DNS and ports 80/443 are working.

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

## Connect paired devices

Pair the orchestrator and worker on one LAN first. Then install or reinstall
each macOS daemon with the public endpoints (use `--role worker` on workers):

```bash
./packaging/macos/install.sh \
  --role orchestrator \
  --connectivity-url https://connect.example.com \
  --stun-server stun:turn.example.com:3478
```

For a manually managed macOS, Windows, or Linux daemon, pass the equivalent
flags directly:

```bash
computehopd \
  --role worker \
  --connectivity-url https://connect.example.com \
  --stun-server stun:turn.example.com:3478
```

Move one device to another network and run `computehop devices` on the
orchestrator. A working NAT traversal path appears as `remote` with `direct` or
`direct (STUN)`. Verify the full path with:

```bash
computehop run --on "Gaming PC" /bin/hostname
```

This exercises direct ICE only. Do not copy the coturn shared secret to a
client to force relay mode.

`verify.sh` performs an authenticated TURN allocation locally without copying
the shared secret off the VPS. A production client must receive short-lived
TURN credentials rather than that shared secret. Issuance is intentionally not
implemented yet: an anonymous pair route proves that two peers share a secret,
but anyone can create a new route and therefore it cannot authorize consumption
of a paid public relay. Before shared staging or production, add a
server-verifiable, expiring, revocable entitlement with quotas. A single-owner
self-hosted deployment may instead use operator-provisioned credentials. A
successful health endpoint and STUN response still do not prove the public
relay path works, so a
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

This stack makes the public services deployable. The daemon now supervises
active pair records, exchanges versioned pair-encrypted ICE descriptions,
selects a path, and runs the existing identity-pinned QUIC control protocol over
it. Job routing prefers LAN and falls back to a ready direct internet path.
Automated and local end-to-end tests pass, but physical unrelated-network
validation still remains. The hosted service does not issue TURN credentials,
so relay fallback is not yet enabled for shared staging or production.
