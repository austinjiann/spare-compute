# Rendezvous deployment

This image runs the payload-opaque ComputeHop rendezvous service. It keeps only
short-lived in-memory pair routes, encrypted presence payloads, encrypted
signals, and bearer-token digests. It has no SQLite database and no access to
jobs, device identities, public keys, project data, logs, or artifacts.

Build it from the repository root:

```bash
docker build -f deploy/connectivity/Dockerfile -t computehop-connectivity:dev .
docker run --read-only --cap-drop ALL -p 127.0.0.1:8080:8080 computehop-connectivity:dev
curl http://127.0.0.1:8080/healthz
```

The binary serves plain HTTP because production TLS terminates at the platform
load balancer or reverse proxy. Never expose that plain HTTP port directly to
the internet. The edge must enforce HTTPS, request/body limits, connection and
source-address rate limits, and must not emit authorization headers or request
bodies into logs.

The current store is process-local. A staging deployment should use one
instance. Multiple replicas require consistent routing by the opaque route ID
or a future shared ephemeral store; ordinary round-robin balancing would split
paired endpoints. TURN is a separate service and is not included in this image.

The binary listens on `:$PORT` when a platform supplies `PORT`, and otherwise
uses `:8080`. An explicit `--listen` flag takes precedence. The Railway staging
runbook is in [`../staging/README.md`](../staging/README.md).
