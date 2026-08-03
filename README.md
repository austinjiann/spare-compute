# ComputeHop

Use the computers you already own as one personal compute pool.

ComputeHop is a local-first background job runner. A Mac acts as the control
computer, and paired workers can run commands, stream logs, keep durable job
state, and return declared outputs.

Current focus:

- macOS control computer
- macOS, Linux, and Windows worker packaging paths
- LAN pairing/discovery first
- optional VPS connectivity foundation for later off-LAN use
- CLI, macOS menu-bar app, and Electron Control Center

## What works now

ComputeHop can already run a real command on another paired computer.

The tested path is:

1. Start one computer as the control Mac.
2. Start another computer as a worker.
3. Pair both devices while they are on the same LAN.
4. Run a smoke test or submit a command to the worker.
5. Stream logs and inspect durable job history.

If the smoke test prints the worker's hostname, the command ran on the other
computer.

```bash
go run ./cmd/computehop smoke
```

Equivalent explicit check:

```bash
go run ./cmd/computehop run --on auto --no-project --follow hostname
```

Project work is also supported. ComputeHop snapshots the selected folder,
uploads missing chunks to the worker, runs from an isolated worker workspace,
and can restore declared outputs.
Snapshots respect `.gitignore`/`.computehopignore` and skip common local
secrets, dependency folders, and caches by default; use `.computehopignore`
negations only for deliberate exceptions such as `.env.example`.

```bash
go run ./cmd/computehop run --on auto -C . --follow go test ./...
go run ./cmd/computehop run --on auto -C . -o dist --follow --get npm run build
```

## Development quick start

Install Go, then start the daemon on the control Mac:

```bash
go run ./cmd/computehopd --role orchestrator --device-name "My Mac"
```

On the worker computer:

```bash
go run ./cmd/computehopd --role worker --device-name "Gaming PC"
```

From the control Mac, discover and pair:

```bash
go run ./cmd/computehop devices
go run ./cmd/computehop connect nearby
go run ./cmd/computehop connect confirm
```

Confirm the same code on the worker:

```bash
go run ./cmd/computehop connect confirm
```

Then prove remote execution:

```bash
go run ./cmd/computehop smoke
```

## Apps

The macOS menu bar is the fast surface for status, device choice, and simple
task submission:

```bash
swift run ComputeHop
```

From the menu bar you can pick a paired worker and ask for safe utility tasks
such as `test connection`, `run hostname`, or `go version` without choosing a
project folder. After a connect flow completes, the menu automatically targets
the connected worker when it is the only runnable worker. Project tasks such as
CI, tests, builds, and packaging still
require selecting the project folder so ComputeHop can snapshot and upload the
right files.

The Electron Control Center is the larger settings surface for setup, device
sync, allowed work, project selection, AI planner configuration, job history,
logs, cancellation, and output restore:

```bash
cd apps/control-center
npm install
npm run dev
```

Build a local unpacked Control Center app bundle with the daemon included:

```bash
npm --prefix apps/control-center run package:dir
```

Build a copyable macOS developer package:

```bash
make macos-archive
```

## Current limits

- Workers must already have the tools needed by the job, such as Go, Node,
  Docker, FFmpeg, or Ollama. Daemons now report common installed tools as
  scheduling/UI hints, and planned CI/package/script work carries conservative
  required-tool hints through submission. Remote submission avoids workers that
  explicitly report any planned required tool or command executable is missing
  before uploading a project. ComputeHop does not install missing tools.
- Native execution is implemented; container/sandbox execution is future work.
- LAN pairing/discovery is the default path. VPS/relay setup exists for staging
  but still needs production validation.
- GitHub Actions may fail before runner startup if the repository owner's
  billing or Actions spending limit blocks runners. That is external to the
  code path; use local validation until billing is fixed.
- The optional AI planner can translate plain-language requests only when an
  OpenAI-compatible API key is configured. The app can save the key/base URL, or
  you can use `COMPUTEHOP_AI_API_KEY` / `COMPUTEHOP_AI_BASE_URL`
  (`OPENAI_API_KEY` / `OPENAI_BASE_URL` still work). Deterministic local
  planning works without a key for common checks, tests, builds, lint, Docker,
  and package tasks.

## Validation

Run the main local gate before opening or merging a PR:

```bash
make pr-check
```

Run the package/release gate before handing artifacts to another machine:

```bash
make release-check
```

This runs the PR gate, builds and smokes the copyable macOS archive, and builds
and verifies Linux/Windows worker archives.

Useful app-specific gates:

```bash
npm --prefix apps/control-center run lint
npm --prefix apps/control-center test
swift test
```

## Documentation

- [Detailed plan](docs/PLAN.md)
- [Control Center](apps/control-center/README.md)
- [macOS app](apps/macos/README.md)
- [Packaging](packaging/README.md)
- [VPS staging](deploy/README.md)
