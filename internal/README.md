# Internal architecture

The Go backend uses inward-facing dependencies:

```text
cmd -> app -> job/device/trust/scheduler/etc.
  \-> infra -> interfaces owned by the packages above
```

- `app/` coordinates complete orchestrator, worker, and connectivity workflows.
- Core packages such as `job/`, `device/`, `connectivity/`, and `scheduler/` own rules and the
  interfaces required to persist or execute them.
- `infra/` implements those interfaces with SQLite, QUIC, mDNS, operating-system
  processes, content storage, and other external technologies.
- `platform/` isolates small operating-system-specific behaviors.
- `workload/` adds Cargo, FFmpeg, and Ollama knowledge on top of the generic job
  lifecycle.

Core packages must not import `infra/`, generated Protobuf packages, Swift code,
or concrete operating-system integrations. Avoid generic `common`, `shared`, or
`utils` packages.
