# Protocol API

Versioned Protocol Buffer definitions live under `proto/computehop/`. Generated
code belongs in `gen/` and must not be edited by hand. Run `make proto` from the
repository root after changing a schema.

Protocol messages are boundary types. They are converted to internal domain
types before application rules are evaluated.

The connectivity schema carries only encrypted, bounded presence and signaling
payloads. Durable device identifiers, pair IDs, jobs, logs, and artifacts do
not belong in the hosted-service protocol.
