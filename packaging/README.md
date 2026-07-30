# Packaging

Native installer definitions and platform release metadata live here. Build
outputs do not belong in source control.

The current macOS developer bundle and per-user launchd installer live in
[`macos/`](macos/), including a non-mutating archive smoke test for copied app
and installer dry-runs. Copyable Linux and Windows worker developer packages
live in [`workers/`](workers/).
