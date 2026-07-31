const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");
const {
  daemonOutputPath,
  executableName,
  goBuildArguments,
  goBuildEnvironment
} = require("./bundle-daemon");

test("daemonOutputPath stages the daemon under Electron resources", () => {
  const root = path.join("tmp", "control-center");

  assert.equal(
    daemonOutputPath({ controlCenterRoot: root, platform: "darwin" }),
    path.join(root, "resources", "bin", "computehopd")
  );
  assert.equal(
    daemonOutputPath({ controlCenterRoot: root, platform: "win32" }),
    path.join(root, "resources", "bin", "computehopd.exe")
  );
});

test("goBuildArguments builds only computehopd with release metadata", () => {
  const output = path.join("tmp", "control-center", "resources", "bin", executableName("computehopd", "linux"));
  const args = goBuildArguments({ version: "1.2.3" }, output);

  assert.deepEqual(args, [
    "build",
    "-trimpath",
    "-ldflags",
    "-s -w -X main.version=1.2.3",
    "-o",
    output,
    "./cmd/computehopd"
  ]);
});

test("goBuildEnvironment supports explicit target platform", () => {
  const env = goBuildEnvironment({ targetOS: "linux", targetArch: "arm64" });

  assert.equal(env.GOOS, "linux");
  assert.equal(env.GOARCH, "arm64");
});
