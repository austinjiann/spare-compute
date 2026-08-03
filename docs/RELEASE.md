# ComputeHop release process

This process keeps release metadata consistent across the CLI, daemon, macOS
bundle, Electron Control Center, and worker archives.

## Version sources

- `VERSION` is the source of truth for the next artifact version.
- `apps/control-center/package.json` and `package-lock.json` must match
  `VERSION`.
- `packaging/macos/Info.plist` must keep the same
  `CFBundleShortVersionString`.
- Go binaries receive the version through `-ldflags -X main.version=...`.
- `COMPUTEHOP_BUILD_NUMBER` is the macOS bundle build number. It must be a
  positive integer and should increase for every notarized macOS upload.

Run the consistency check before any release:

```bash
make release-version-check
```

## Cut a developer/private-beta release

1. Start from an up-to-date `main` checkout.
2. Pick the next semantic version and update:
   - `VERSION`
   - `CHANGELOG.md`
   - `apps/control-center/package.json`
   - `apps/control-center/package-lock.json`
   - `packaging/macos/Info.plist`
3. Run:

   ```bash
   make release-check
   ```

4. Build artifacts with explicit release metadata:

   ```bash
   COMPUTEHOP_VERSION="$(tr -d '\r\n' < VERSION)" \
   COMPUTEHOP_BUILD_NUMBER=1 \
   make macos-archive worker-archives
   ```

5. Verify the checksums in `dist/macos/` and `dist/workers/`.
6. Tag the exact commit:

   ```bash
   git tag "v$(tr -d '\r\n' < VERSION)"
   git push origin "v$(tr -d '\r\n' < VERSION)"
   ```

7. Upload the archives and checksum files to a draft GitHub release.

Developer/private-beta artifacts are ad-hoc signed and are not notarized. Do
not call them public release artifacts.

## Public release gate

Before publishing for non-contributors:

- replace ad-hoc signing with Developer ID signing;
- notarize and staple the macOS app;
- decide whether Windows/Linux workers ship as archives, installers, or both;
- run the full clean-machine acceptance matrix in `docs/LAUNCH_CHECKLIST.md`;
- record supported architectures and known limitations in the GitHub release
  notes.

## Changelog rules

- Keep an `Unreleased` section at the top.
- Use user-visible categories when useful: Added, Changed, Fixed, Security,
  Packaging, Known limitations.
- Mention migration or compatibility impact explicitly.
- Do not include raw secrets, local machine names, pairing codes, or private
  support-bundle details.
