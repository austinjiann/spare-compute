# Scripts

Repository automation that is useful to developers and CI belongs here. Scripts
must be non-interactive where practical and safe to rerun.

## Local launch validation

Run targeted local launch checks from the repository root:

```bash
make launch-local-validation
```

This runs CLI, daemon, snapshot, Control Center, dependency-audit, and
setup-screenshot checks that support launch readiness. It does not replace
physical packaged-app validation on separate macOS, Linux, or Windows machines.
