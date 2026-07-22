# ComputeHop for macOS

The macOS application is a presentation-only SwiftUI menu-bar client. Durable
jobs, discovery, trust, and remote sessions stay in the Go daemon, so closing
the menu does not stop work.

For development, start the daemon in one terminal and the menu-bar app in
another:

```bash
go run ./cmd/computehopd --role orchestrator --device-name "My Mac"
swift run ComputeHop
```

The app reads the daemon's owner-only capability token and connects to its Unix
socket under `~/Library/Application Support/ComputeHop`. Protocol Buffer models
come from `gen/swift`; regenerate them with `make proto` after schema changes.

`swift run` is the development path. Signing, notarization, a bundled daemon,
and launchd installation belong to the later macOS packaging slice.
