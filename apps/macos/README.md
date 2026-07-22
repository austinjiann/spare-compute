# ComputeHop for macOS

The macOS application is a presentation-only SwiftUI menu-bar client. Durable
jobs, discovery, trust, and remote sessions stay in the Go daemon, so closing
the menu does not stop work.

The current menu supports daemon health with local Mac identity, first-run
next-step guidance that points at `computehop setup worker --device-name
"Gaming PC"` and its `--lan-only` variant when no worker exists, Copy buttons
for setup commands, a one-click Connect Nearby Worker action when exactly one
unpaired worker is visible, nearby and connected devices, LAN-only status for
paired workers whose remote connectivity is disabled, two-sided connect
confirmation with explicit local/other-device status, stale restart duplicate
suppression for trusted nearby devices,
native job submission to this Mac, Auto worker when exactly one
worker is runnable, or a paired available worker, a Smoke Test button that runs
`hostname` remotely without uploading a project, recent jobs, reconnectable
output, copyable equivalent `computehop run ...` commands from the run form,
explicit command-input validation for unfinished quotes and escapes, explicit
no-stdout/stderr placeholders for running and finished jobs, a copyable
`computehop logs --follow <job-id>` handoff for terminal debugging,
job-completion notifications for observed running jobs, cancellation, declared
output paths, empty-jobs hints, and
conflict-safe artifact restoration through a native destination picker. Output
retrieval errors explain not-ready, missing, and undeclared outputs instead of
showing raw daemon messages. When Run is disabled, the menu explains whether
the daemon is offline, the command is empty, or a remote project folder must be
chosen before upload. The
command field splits quotes and escapes into literal arguments but never invokes
a shell or performs shell expansion. Local jobs default to the user's home
directory. For a remote job, choose a project folder on this Mac; the daemon
incrementally snapshots it and executes from an isolated worker workspace.
Output declarations are comma-separated portable paths relative to that
workspace; the Outputs button appears after a job with declared outputs
succeeds.

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
