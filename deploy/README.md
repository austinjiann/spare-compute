# Hosted deployment

This directory contains environment-specific configuration for the opaque
connectivity service and will later include coturn and monitoring. It must never
contain production credentials or device-private state.

- `connectivity/` packages the in-memory rendezvous/signaling service and
  documents its TLS-edge and scaling requirements.
