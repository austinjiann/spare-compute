# ComputeHop connectivity operations

This is the operating policy for rendezvous, STUN, and TURN connectivity.

## Product decision

The first public ComputeHop release should be LAN-first and self-hosted for
off-LAN connectivity.

Policy:

- LAN discovery and paired LAN jobs remain the default.
- Self-hosted one-VPS connectivity is supported for technical/private-beta
  users.
- A project-operated hosted connectivity service is allowed later, but it must
  not be exposed broadly until account, entitlement, quota, abuse-prevention,
  monitoring, alerting, backup, and incident-response plans exist.
- If both exist later, the product should support both:
  - project-operated connectivity for users who want “it just works”;
  - self-hosted connectivity for users who do not want project-operated relay
    infrastructure involved.

The connectivity service never receives device private keys, job commands,
project files, job logs, or artifacts. It handles only opaque rendezvous
presence/signaling and TURN packet relay.

## Expected VPS cost

For staging, use one small Ubuntu VPS near the testers:

- 1 shared vCPU
- 1 GiB RAM
- static public IPv4
- at least 500 GiB to 1 TiB included monthly transfer
- provider bandwidth alerts enabled

Planning estimate checked on 2026-08-03:

| Provider example | Entry planning number | Transfer planning note |
| --- | ---: | --- |
| DigitalOcean Basic Droplet | from about $4/month | entry transfer starts around 500 GiB/month |
| Akamai/Linode Nanode 1 GB | about $5/month | commonly listed with 1 TiB/month transfer |
| Hetzner cost-optimized cloud | about €6/month before VAT/region extras | public IPv4 can be a separate monthly line item |

Use these as order-of-magnitude planning numbers only. Check live provider
pricing before purchasing:

- <https://www.digitalocean.com/pricing/droplets>
- <https://www.akamai.com/cloud/pricing>
- <https://www.hetzner.com/cloud>

For one owner and light staging, expect roughly $5-10/month before domain costs
and bandwidth overage.

## Bandwidth model

Rendezvous and STUN traffic should be negligible. TURN is the cost risk because
it relays job traffic when a direct path fails.

A forced TURN job can relay approximately:

```text
project upload + command/log traffic + returned outputs + protocol overhead
```

Examples:

- A 200 MiB project snapshot and 20 MiB output can consume roughly 220+ MiB of
  relay transfer.
- A 2 GiB media input and 2 GiB returned output can consume roughly 4+ GiB.
- Repeating a 4 GiB relay path 100 times/month can consume roughly 400+ GiB.

Operational rule: large project snapshots, video exports, model files, and
artifact-heavy workloads should prefer LAN or direct ICE paths. Treat TURN as a
reliability fallback, not as the cheap path for bulk transfer.

## TURN quota policy

Current one-VPS staging supports operator-generated, short-lived TURN
credentials. That is acceptable for a single-owner staging host only.

For staging:

- generate credentials with short TTLs;
- label credentials by tester or validation run;
- do not copy the coturn shared secret to client devices;
- rotate the shared secret after suspected exposure;
- enable provider bandwidth alerts before inviting testers;
- stop issuing credentials if bandwidth usage is unexplained.

Before any shared/project-operated service:

- issue TURN credentials from a server-verifiable entitlement;
- bind credentials to account/device/session scope;
- enforce monthly bandwidth quotas;
- support immediate credential revocation;
- rate-limit failed allocation attempts;
- monitor relay allocation volume and egress;
- document abuse handling and incident response.

## Monitoring and alerts

Minimum staging checks:

- `deploy/vps/verify.sh` after deploys and credential rotation;
- HTTPS health check for the rendezvous service;
- coturn allocation check from the VPS;
- provider bandwidth alert at 50%, 80%, and 100% of included transfer;
- Docker service restart policy enabled through Compose.

Project-operated connectivity needs stronger monitoring before launch:

- health and latency alerts;
- TURN allocation count and egress dashboards;
- quota burn-rate alerts;
- abuse anomaly alerts;
- backup/restore for operator configuration and secrets;
- incident-response runbook with customer-visible status updates.

## Recovery runbook

When off-LAN connectivity fails:

1. Verify DNS for both hostnames.
2. Verify provider firewall and host firewall ports.
3. Run:

   ```bash
   ./deploy/vps/verify.sh
   docker compose --project-directory deploy/vps ps
   docker compose --project-directory deploy/vps logs --tail=100 rendezvous caddy coturn
   ```

4. Restart only the unhealthy service first:

   ```bash
   docker compose --project-directory deploy/vps restart rendezvous
   docker compose --project-directory deploy/vps restart coturn
   ```

5. If TURN credentials may be exposed, rotate
   `deploy/vps/secrets/turn_shared_secret`, recreate coturn, and issue fresh
   short-lived credentials.
6. If a deploy caused the failure, roll back to the last recorded known-good git
   commit and run `deploy/vps/verify.sh` again.
7. Re-test from another network with a utility job before retrying large project
   transfers.
