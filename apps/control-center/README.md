# ComputeHop Control Center

This is the larger settings surface for ComputeHop. The macOS menu bar should
stay small: status, device picker, and quick task entry. Device sync, allowed
work, project sync, relay settings, and future AI planner settings belong here.

Run it in development:

```bash
cd apps/control-center
npm install
npm run dev
```

Current scope:

- connects directly to the local ComputeHop daemon over the owner-only local IPC
  socket;
- can start the local daemon from the app in development or from the bundled
  daemon in packaged builds, either as the Control Mac or as a Worker;
- defaults non-macOS computers to Worker because Control Mac is macOS-only;
- reads trusted and nearby devices from the daemon;
- starts nearby-device pairing from the Devices list;
- shows active pairing verification codes and lets the user confirm or reject
  them in-app;
- forgets paired devices through daemon IPC;
- plans plain-language tasks such as "run tests", "build the app", and
  "check CI" into one safe command using local project rules;
- previews the exact command before running when Preview before running is
  enabled;
- runs a one-click connection test on the selected computer without requiring a
  project folder;
- requires a project folder before sending project-style commands such as tests,
  builds, package scripts, and Makefile targets to another computer;
- declares optional files/folders to bring back from completed jobs;
- submits selected commands as durable native jobs;
- polls daemon job logs and streams stdout/stderr into the window;
- lists recent jobs for the selected computer and opens their persisted logs;
- restores declared outputs from succeeded jobs through daemon IPC;
- cancels running jobs through daemon IPC;
- targets This Mac by default, or a selected connected worker;
- skips project sync for remote utility commands until a project folder is
  selected;
- stores UI-only settings in browser local storage for now.

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
```

Next step: add an optional LLM planner that can explain and compose more complex
multi-step work while keeping the same explicit command preview before
submission.
