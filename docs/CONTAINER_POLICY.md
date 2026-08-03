# ComputeHop container execution policy

Container execution is an advanced/private-beta capability, not the public
default.

## Default public behavior

- Public task planning should default to native execution.
- Docker and Compose build requests should run as normal native commands such
  as `docker build .` or `docker compose build` on a computer that already has
  Docker/Podman installed.
- ComputeHop must not silently convert a normal task into a container job.
- Container jobs require an explicit container executor request and image.
- If a selected worker does not advertise container support, the UI/CLI should
  fail before upload and tell the user to choose another computer or use a
  normal command.

## Engine support

Workers may advertise container execution only when a Docker- or
Podman-compatible Engine API responds successfully.

ComputeHop:

- uses the Engine API rather than parsing `docker` or `podman` CLI output;
- pulls missing images before creating a container;
- streams pull progress and process output through the normal job surfaces;
- removes one-shot containers after cancellation or terminal completion.

ComputeHop does not install or manage:

- Docker Desktop;
- Podman;
- Linux VMs;
- GPU/container runtime integration;
- image registries;
- credential helpers.

## Private registries

Private registry authentication is explicitly out of scope for the public
default.

If a worker can pull from a private registry, that access must already be
configured on that worker through Docker/Podman’s normal login or credential
helper flow. ComputeHop does not store registry usernames, passwords, tokens, or
pull secrets, and it does not send registry credentials through the
orchestrator, relay, or job spec.

Diagnostics must not include raw container image references because private
registry hostnames and repository paths can reveal internal project names.

## Sandboxing boundary

Containers are for dependency consistency. They are not currently a ComputeHop
security sandbox.

Current container jobs may:

- run with the local container engine’s default isolation;
- bind-mount the selected project as `/workspace`;
- use the worker’s existing engine network and registry configuration;
- consume worker CPU, memory, disk, and network according to engine defaults.

Before making container execution a consumer-facing default, ComputeHop needs a
separate hardening pass for:

- resource limits;
- read-only/project-scoped mounts where possible;
- network policy;
- non-root/user namespace behavior;
- GPU access policy;
- private registry UX;
- image allow/deny lists;
- clean-machine validation on macOS, Linux, and Windows workers.

Until those are done, native execution remains the default and container
execution stays behind explicit/advanced paths.
