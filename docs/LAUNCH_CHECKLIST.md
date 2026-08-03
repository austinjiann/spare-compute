# ComputeHop launch checklist

This checklist defines what “complete” means for ComputeHop. It is intentionally
stricter than “the code path exists”: every launch gate must be validated from
packaged apps or archives, not only from `go run`, `swift run`, or unit tests.

## Product stages

### Developer alpha

Goal: prove the core architecture from a source checkout.

- [x] Local daemon with authenticated local IPC.
- [x] Durable local job execution, logs, cancellation, and restart
  reconciliation.
- [x] LAN discovery with privacy-safe mDNS advertisements.
- [x] Two-sided device pairing with persistent trust and revocation.
- [x] Remote command submission to an explicit paired LAN worker.
- [x] Durable remote job routing by job ID.
- [x] Project snapshot upload, isolated worker execution, declared output
  collection, and output restore.
- [x] macOS menu-bar app can submit simple local and remote tasks.
- [x] Electron Control Center can start daemons, connect workers, configure
  planner settings, run tasks, inspect jobs, cancel, and restore outputs.
- [x] Automated Go, Swift, Control Center, packaging-script, deploy-script, and
  worker-archive checks pass locally.

### Private beta

Goal: a technical user can install ComputeHop without a source checkout and run
real work across machines on the same LAN.

- [ ] Merge all open UI/packaging cleanup PRs.
- [ ] Build the macOS developer archive from a clean checkout.
- [ ] Install the packaged macOS app as the control Mac using `install.sh`.
- [ ] Install the packaged macOS app as a worker on a second Mac using
  `install.sh`.
- [ ] Pair the two packaged Mac installs from the UI and from the CLI.
- [ ] Run packaged-app smoke tests:
  - [ ] `hostname` or equivalent utility job.
  - [ ] long-running job cancellation.
  - [ ] daemon restart while a job is running.
  - [ ] reconnectable logs after control app restart.
  - [ ] project test/build command with snapshot upload.
  - [ ] declared output restore.
- [ ] Build Linux and Windows worker archives from a clean checkout.
- [ ] Install and run a Linux worker from the archive on a real Linux machine.
- [ ] Install and run a Windows worker from the archive on a real Windows
  machine.
- [ ] Validate Mac control → Linux worker:
  - [ ] discovery or documented connection path.
  - [ ] pairing.
  - [ ] smoke job.
  - [ ] project job.
  - [ ] cancellation.
  - [ ] logs.
- [ ] Validate Mac control → Windows worker with the same matrix.
- [ ] Verify duplicate device names and stale/offline devices produce clear UI
  and CLI behavior.
- [ ] Verify missing tool errors before uploading a project where the worker
  reports capabilities.
- [ ] Verify `.gitignore` and `.computehopignore` behavior on a real project.
- [ ] Verify default secret exclusions using representative files such as
  `.env`, private keys, dependency folders, build output folders, and caches.
- [ ] Confirm the Control Center first-run surface works with no terminal
  commands.
- [ ] Confirm uninstall preserves or removes only the documented files.

### Off-LAN staging

Goal: paired devices keep working when they are no longer on the same network.

- [ ] Buy or provision a small Ubuntu VPS.
- [ ] Point DNS at the VPS for rendezvous and TURN hostnames.
- [ ] Run `deploy/vps/init.sh` on an operator machine and copy generated files
  to the VPS.
- [ ] Bootstrap the VPS with Caddy, coturn, and the rendezvous service.
- [ ] Verify HTTPS health checks and TURN listener checks.
- [ ] Pair devices on the LAN with connectivity secrets enabled.
- [ ] Move the worker to an unrelated network and validate:
  - [ ] direct ICE path when available.
  - [ ] forced TURN relay path.
  - [ ] path recovery after Wi-Fi change.
  - [ ] daemon restart and reconnect.
  - [ ] job submission, logs, cancellation, snapshot upload, and output restore.
- [ ] Confirm the relay cannot read job commands, project files, logs, or
  artifacts.
- [ ] Document expected VPS cost, bandwidth limits, TURN quotas, and operational
  recovery steps.

### Public launch

Goal: a non-contributor can install, understand, trust, and recover ComputeHop.

- [ ] Replace development/ad-hoc packaging with release signing.
- [ ] Notarize and staple the macOS app.
- [ ] Produce universal macOS release artifacts or clearly document supported
  architectures.
- [ ] Decide whether Windows/Linux workers ship as archives, installers, or both.
- [ ] Add upgrade-safe install behavior for all supported platforms.
- [ ] Add release versioning and changelog process.
- [ ] Run full clean-machine validation for every release artifact.
- [ ] Add public-facing setup docs with screenshots.
- [ ] Add troubleshooting docs for:
  - [ ] daemon already running.
  - [ ] duplicate devices.
  - [ ] pairing code mismatch.
  - [ ] worker offline.
  - [ ] missing tools.
  - [ ] project upload failure.
  - [ ] output restore conflicts.
  - [ ] CI blocked by GitHub Actions billing.
- [ ] Add security docs that explain the pairing trust boundary and command
  execution risk in plain language.
- [ ] Add clear UI affordances for revoking a trusted worker.
- [ ] Add crash/diagnostic bundle collection that redacts secrets.
- [ ] Decide the policy for container execution, private registries, and
  sandboxing before exposing container tasks as a public default.
- [ ] Decide whether the hosted connectivity service is operated by the project,
  self-hosted by users, or both.
- [ ] If the project operates connectivity infrastructure, add account,
  entitlement, quota, abuse-prevention, monitoring, alerting, backup, and
  incident-response plans.

## Release acceptance matrix

Every checked release should record results for this matrix.

| Scenario | macOS worker | Linux worker | Windows worker |
| --- | --- | --- | --- |
| Fresh install from artifact | Pending | Pending | Pending |
| Pair from clean state | Pending | Pending | Pending |
| Utility smoke job | Pending | Pending | Pending |
| Project snapshot job | Pending | Pending | Pending |
| Declared output restore | Pending | Pending | Pending |
| Running-job cancellation | Pending | Pending | Pending |
| Worker daemon restart | Pending | Pending | Pending |
| Control app restart | Pending | Pending | Pending |
| Offline/stale device recovery | Pending | Pending | Pending |
| Uninstall path | Pending | Pending | Pending |

## Non-blocking after public launch

These are useful but should not block the first public release unless product
scope changes.

- Byte-range upload/download resume.
- Rich per-adapter progress parsing.
- Full Windows tray app.
- Hosted account system for managed relay entitlements.
- Multi-orchestrator ownership.
- Multi-user or team scheduling.
- Distributed-memory or interactive-shell features.
