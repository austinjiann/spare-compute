# Packaging

Native installer definitions and platform release metadata live here. Build
outputs do not belong in source control.

The current macOS developer bundle and per-user launchd installer live in
[`macos/`](macos/), including a non-mutating archive smoke test for copied app
and installer dry-runs. Copyable Linux and Windows worker developer packages
live in [`workers/`](workers/), including non-mutating worker setup checks for
Linux systemd user services and Windows scheduled tasks.

Before sharing developer artifacts with another machine, run:

```bash
make release-check
```

This combines the main PR validation, macOS archive smoke test, and Linux/Windows
worker archive build and verification.
