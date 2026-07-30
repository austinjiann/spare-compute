const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  controlCenterBackgroundPaths,
  verifyControlCenterBackground
} = require("../../../packaging/macos/verify-control-center-background");

test("verifyControlCenterBackground requires the embedded Control Center to resolve the parent daemon", async (t) => {
  const root = await tempDirectory(t);
  const appBundle = path.join(root, "ComputeHop.app");
  const paths = controlCenterBackgroundPaths(appBundle);

  await executable(paths.parentDaemon);
  await executable(paths.controlCenterDaemon);

  const result = await verifyControlCenterBackground(appBundle);

  assert.equal(result.ok, true);
  assert.equal(result.parentDaemon, paths.parentDaemon);
  assert.equal(result.controlCenterDaemon, paths.controlCenterDaemon);
  assert.equal(result.resolvedDaemon, paths.parentDaemon);
});

test("verifyControlCenterBackground rejects nested-daemon resolution", async (t) => {
  const root = await tempDirectory(t);
  const appBundle = path.join(root, "ComputeHop.app");
  const paths = controlCenterBackgroundPaths(appBundle);

  await executable(paths.parentDaemon);
  await executable(paths.controlCenterDaemon);

  await assert.rejects(
    () => verifyControlCenterBackground(appBundle, {
      service: {
        parentAppDaemonPath: () => paths.parentDaemon,
        resolveDaemonExecutable: async () => paths.controlCenterDaemon
      }
    }),
    /nested daemon/
  );
});

test("verifyControlCenterBackground reports missing daemon binaries", async (t) => {
  const root = await tempDirectory(t);
  const appBundle = path.join(root, "ComputeHop.app");

  await assert.rejects(
    () => verifyControlCenterBackground(appBundle),
    /parent app daemon is missing/
  );
});

async function tempDirectory(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-macos-background-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  return root;
}

async function executable(filePath) {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  await fs.writeFile(filePath, "");
  await fs.chmod(filePath, 0o755);
}
