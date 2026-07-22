# ComputeHop for macOS

The macOS application is a presentation-only SwiftUI menu-bar client. Durable
jobs, discovery, trust, and remote sessions stay in the Go daemon, so closing
the menu does not stop work.

The current menu supports daemon health, nearby and paired devices, two-sided
pairing confirmation, native job submission to this Mac or a paired available
worker, recent jobs, reconnectable output, and cancellation. The command field
splits quotes and escapes into literal arguments but never invokes a shell or
performs shell expansion. Local jobs default to the user's home directory. For
a remote job, choose a project folder on this Mac; the daemon incrementally
snapshots it and executes from an isolated worker workspace.

For development, start the daemon in one terminal and the menu-bar app in
another:

```bash
go run ./cmd/computehopd --role orchestrator --device-name "My Mac"
swift run ComputeHop
```

The app reads the daemon's owner-only capability token and connects to its Unix
socket under `~/Library/Application Support/ComputeHop`. Protocol Buffer models
come from `gen/swift`; regenerate them with `make proto` after schema changes.

`swift run` remains the fastest source-development path. A real ad-hoc-signed
app bundle with both Go binaries and a per-user launchd installer is available
under [`../../packaging/macos`](../../packaging/macos). Developer ID
signing, notarization, universal release binaries, and clean-machine upgrade
testing remain release work. The optional live IPC test can validate submission
and logs against a running daemon:

```bash
COMPUTEHOP_INTEGRATION_STATE_DIR="$HOME/Library/Application Support/ComputeHop" \
COMPUTEHOP_INTEGRATION_SUBMIT=1 swift test
```
