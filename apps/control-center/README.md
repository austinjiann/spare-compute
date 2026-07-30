# ComputeHop Control Center

This is the larger settings surface for ComputeHop. The macOS menu bar should
stay small: status, device picker, and quick task entry. Device sync, allowed
work, project sync, relay settings, and optional AI planner configuration belong
here. The current AI planner path is environment-configured until OS credential
storage exists.

Run it in development:

```bash
cd apps/control-center
npm install
npm run dev
```

Stage the daemon for packaged-app smoke testing:

```bash
npm run bundle-daemon
```

This writes the current platform's `computehopd` binary to
`apps/control-center/resources/bin`, which is the same location the packaged
Control Center launcher expects under Electron's resources directory.

Build an unpacked app directory for the current Mac:

```bash
npm run package:dir
```

This runs `bundle-daemon` first, then writes the app bundle under
`apps/control-center/.out`. The packaged app includes the daemon at
`Contents/Resources/bin/computehopd`, so the **Start** button can launch
ComputeHop without a repository checkout.

Current scope:

- connects directly to the local ComputeHop daemon over the owner-only local IPC
  socket;
- can start the local daemon from the app in development or from the bundled
  daemon in packaged builds, either as the Control Mac or as a Worker;
- defaults non-macOS computers to Worker because Control Mac is macOS-only;
- labels the local computer from the daemon's actual identity and role;
- can stage the bundled daemon binary used by packaged Control Center builds;
- can package an unpacked current-platform app directory with that daemon
  copied into Electron's runtime resources;
- reads trusted and nearby devices from the daemon;
- starts nearby-device pairing from the Devices list;
- shows active pairing verification codes and lets the user confirm or reject
  them in-app;
- forgets paired devices through daemon IPC;
- clears local sync and allowed-work overrides when a paired device is
  forgotten;
- can disable or re-enable paired workers so disabled devices stay visible but
  cannot be selected, used by Auto worker, or used as run targets;
- stores allowed work categories per selected device, so This Mac and each
  worker can have different Builds/Tests/Docker/AI/Video/Exact-command policy;
- plans plain-language tasks such as "run tests", "build the app", and
  "check CI" into one safe command using local project rules;
- uses deterministic local planning first, so no API key is required for normal
  Check/Test/Build/Lint/Docker planning;
- can fall back to an optional OpenAI Responses API planner for tasks local
  rules cannot map when `OPENAI_API_KEY` is present; set
  `COMPUTEHOP_OPENAI_MODEL` to override the default model;
- keeps AI-planned unknown commands behind the same disabled-by-default
  **Exact commands** allowance and rejects shell operators, multiline commands,
  privilege escalation, and destructive removal before preview;
- maps lint/style requests to conventional Go, Rust, or Python quality commands
  when no package script or Makefile target exists;
- maps Docker/Compose build requests to `docker build .` or
  `docker compose build` when matching project files are present;
- suggests project-aware task chips such as Check, Test, Build, Lint, and
  Docker after a project folder is selected;
- lets the selected project folder be cleared so utility runs stay visibly
  projectless;
- filters suggested work and blocks planned submissions when the matching
  selected-device Allow checkbox is turned off;
- keeps arbitrary exact commands behind the disabled-by-default **Exact commands**
  allowance while still letting recognized typed commands use their specific
  Builds/Tests/Docker/AI/Video category;
- parses exact commands with quote/escape handling before submitting them to the
  daemon;
- displays job-history commands with quoting for spaces and empty arguments;
- previews the exact command before running when Preview before running is
  enabled;
- runs a one-click connection test on the selected computer without requiring a
  project folder;
- requires a project folder before sending project-style commands such as tests,
  builds, package scripts, and Makefile targets to another computer;
- covers remote run request construction so project commands upload a selected
  folder, utility commands stay projectless, and Auto/explicit worker selectors
  are preserved;
- declares optional files/folders to bring back from completed jobs;
- submits selected commands as durable native jobs;
- polls daemon job logs and streams stdout/stderr into the window;
- lists recent jobs for the selected computer and opens their persisted logs;
- updates the recent-job row immediately when a UI-submitted job reaches a
  terminal state, before the next history refresh;
- restores declared outputs from succeeded jobs through daemon IPC;
- cancels running jobs through daemon IPC;
- recovers the Run button when a stop request races with an already-finished or
  no-longer-tracked run;
- targets This Mac by default, or a selected connected worker;
- offers an **Auto worker** run target when exactly one connected worker is
  available, matching the CLI's `--on auto` behavior;
- resolves Auto worker job history to the backing worker so browsing logs and
  outputs does not depend on re-running automatic worker selection;
- routes logs, cancellation, and output restore for Auto-worker jobs through the
  daemon's remembered job placement instead of re-resolving Auto later;
- skips project sync for remote utility commands until a project folder is
  selected;
- does not silently substitute the development checkout as the working directory
  when the UI says no project is selected;
- keeps projectless utility jobs projectless when a project folder is selected
  unless outputs are declared; connection tests always stay projectless and
  ignore output-return settings;
- tells the user when a remote project run is snapshotting/uploading before the
  job is submitted;
- stores Control Center preferences in the app user-data directory, with
  browser local storage kept only as a migration/fallback path.

Manual two-computer check:

1. Start the daemon or click **Start** with **Control Mac** selected in Control
   Center on the orchestrator Mac.
2. On the second computer, open Control Center, choose **Worker**, and click
   **Start**.
3. Open Control Center and connect the nearby worker from **Devices**.
4. Confirm the same pairing code on both computers.
5. Select the worker and click **Test worker**. A successful check prints the
   worker's hostname in the job output and adds a succeeded recent job.
6. Choose a project, enter `run tests` or an exact command such as
   `go test ./...`, preview the plan, then run it on the selected worker.
7. If outputs were declared before submission, use **Outputs** on the succeeded
   job row to restore them to a chosen local folder.

Known current boundary: GitHub Actions checks may fail before runner startup if
the GitHub account billing/spending limit blocks Actions minutes. That does not
exercise the app code; use the local commands below for code validation.

Validation:

```bash
npm --prefix apps/control-center run lint
npm --prefix apps/control-center test
npm --prefix apps/control-center run package:dir
```

Next step: store optional AI planner credentials in the OS credential store
instead of relying on process environment variables.
