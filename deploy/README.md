# Hosted deployment

This directory contains environment-specific configuration for the opaque
connectivity service and will later include coturn and monitoring. It must never
contain production credentials or device-private state.

- `connectivity/` packages the in-memory rendezvous/signaling service and
  documents its TLS-edge and scaling requirements.
- `vps/` runs the staging rendezvous, HTTPS edge, STUN, and authenticated TURN
  relay together on one small provider-neutral Linux VPS, with an initializer
  that writes the local `.env` and server-only TURN shared secret after the host
  is purchased, plus a helper that derives short-lived operator-provisioned TURN
  username/password credentials for single-owner relay testing.

Operational policy, expected costs, bandwidth planning, TURN quota policy, and
recovery steps are documented in
[`../docs/CONNECTIVITY_OPERATIONS.md`](../docs/CONNECTIVITY_OPERATIONS.md).
