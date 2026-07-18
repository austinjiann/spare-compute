# Commands

This directory contains composition roots only:

- `computehop` is the local CLI and talks to `computehopd` over local IPC.
- `computehopd` runs in orchestrator, worker, or combined mode.
- `computehop-connectivity` provides hosted rendezvous and signaling.

Command packages should parse startup configuration, construct dependencies,
and start processes. Product logic belongs under `internal/`.
