# ComputeHop physical validation

Use this document for the launch gates that cannot be proven by unit tests,
`go run`, `swift run`, or a single local Mac. Every pass or failure should be
recorded in `docs/RELEASE_VALIDATION.md` with the commit SHA, machine details,
artifact checksums, commands, and result.

The command-line entry point is:

```bash
computehop setup launch --worker-name "Austin MacBook 2"
```

That command prints the current release-validation handoff and points each
remaining gate at the exact setup command to run.

## Evidence rule

Do not check a physical gate in `docs/LAUNCH_CHECKLIST.md` until evidence exists
in `docs/RELEASE_VALIDATION.md`.

At minimum, record:

- commit SHA;
- artifact filename and SHA-256;
- operating system, CPU architecture, and device role;
- exact commands run;
- key outputs from `computehop status`, `computehop devices`, `computehop jobs`,
  `computehop logs`, package validators, and VPS verifiers;
- whether the run passed, failed, or needs a retry.

## macOS packaged LAN validation

Use this for the second-Mac private-beta gates:

```bash
make macos-archive
computehop setup smoke --worker-name "Austin MacBook 2"
```

Run the printed commands from the copied `ComputeHop-macos.zip` package, not
from a source checkout on the second Mac.

Required evidence:

- orchestrator package install and `validate-installed.sh --role orchestrator`;
- worker package install and `validate-installed.sh --role worker`;
- UI and CLI pairing;
- remote utility job;
- cancellation;
- worker daemon restart while a job is running;
- reconnectable logs after restarting the control app;
- project snapshot run;
- declared output restore;
- final `computehop devices` and `computehop jobs --on auto` output.

## Linux worker validation

Use this for the Mac control → Linux worker gates:

```bash
make worker-archives
computehop setup workers --target linux --device-name "Home Server"
```

Run the printed package validation and worker commands on a real Linux machine.
After pairing from the Mac orchestrator, run the printed evidence-capture
commands. Record:

- archive checksum verification;
- installed-worker validator output;
- pairing result;
- `computehop smoke`;
- project job with declared output restore;
- cancellation;
- logs;
- final worker service status.

## Windows worker validation

Use this for the Mac control → Windows worker gates:

```bash
make worker-archives
computehop setup workers --target windows --device-name "Gaming PC"
```

Run the printed PowerShell package validation and worker commands on a real
Windows machine. After pairing from the Mac orchestrator, run the printed
evidence-capture commands. Record the same matrix as Linux: checksum, validator,
pairing, smoke job, project job, cancellation, logs, and final scheduled-task or
direct-worker status.

## Off-LAN validation

Use this only after same-LAN macOS validation passes:

```bash
computehop setup vps \
  --connectivity-domain connect.example.com \
  --turn-domain turn.example.com \
  --email admin@example.com \
  --public-ip 203.0.113.10
```

Required evidence:

- VPS provider, region, operating system, and monthly transfer assumptions;
- DNS records for rendezvous and TURN hostnames;
- `deploy/vps/init.sh` output with secrets redacted;
- `deploy/vps/bootstrap-ubuntu.sh` output;
- `docker compose --project-directory deploy/vps config --quiet`;
- `docker compose --project-directory deploy/vps up -d --build`;
- `deploy/vps/verify.sh`;
- short-lived TURN credential generation with secrets redacted;
- LAN pairing with connectivity-enabled installs;
- direct ICE path when available;
- forced TURN relay path;
- Wi-Fi/network-change recovery;
- daemon restart and reconnect;
- job submission, logs, cancellation, snapshot upload, and output restore.

The relay must not receive job commands, project files, logs, or artifacts in
plaintext. Record packet/log inspection at the service boundary where practical,
with secrets redacted.

## Signing, notarization, and clean-machine validation

Developer/ad-hoc packaging is not public-release packaging. Public macOS release
validation requires real Apple Developer ID credentials:

```bash
COMPUTEHOP_CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" \
COMPUTEHOP_NOTARY_KEYCHAIN_PROFILE="computehop-notary" \
COMPUTEHOP_BUILD_NUMBER=1 \
make macos-release-archive
```

Required evidence:

- signing identity used, with team-sensitive details redacted where appropriate;
- notarization submission result;
- staple verification;
- clean-machine install with no source checkout;
- first-run Control Center setup without terminal commands;
- uninstall behavior;
- upgrade behavior from the previous release artifact.
