const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");
const {
  createPackageOptions,
  packageApp,
  packageIgnorePatterns,
  stageDaemonForPackage
} = require("./package-app");

test("createPackageOptions builds an unpacked app bundle with daemon resources", () => {
  const root = path.join("tmp", "control-center");
  const options = createPackageOptions({
    controlCenterRoot: root,
    out: path.join(root, "release"),
    platform: "darwin",
    arch: "arm64"
  });

  assert.equal(options.dir, path.resolve(root));
  assert.equal(options.name, "ComputeHop Control Center");
  assert.equal(options.executableName, "ComputeHop");
  assert.equal(options.appBundleId, "com.computehop.controlcenter");
  assert.equal(options.platform, "darwin");
  assert.equal(options.arch, "arm64");
  assert.equal(options.asar, true);
  assert.equal(options.prune, true);
  assert.deepEqual(options.extraResource, [path.join(path.resolve(root), "resources", "bin")]);
});

test("createPackageOptions defaults to a hidden output directory ignored by Go tooling", () => {
  const root = path.join("tmp", "control-center");
  const options = createPackageOptions({
    controlCenterRoot: root,
    platform: "darwin",
    arch: "arm64"
  });

  assert.equal(options.out, path.join(path.resolve(root), ".out"));
});

test("packageIgnorePatterns excludes generated outputs and copied daemon source", () => {
  const patterns = packageIgnorePatterns();

  assert.equal(matchesAny(patterns, "/.out/ComputeHop-darwin-arm64"), true);
  assert.equal(matchesAny(patterns, "/out/ComputeHop-darwin-arm64"), true);
  assert.equal(matchesAny(patterns, "/dist/ComputeHop.dmg"), true);
  assert.equal(matchesAny(patterns, "/resources/bin/computehopd"), true);
  assert.equal(matchesAny(patterns, "/src/main.test.js"), true);
  assert.equal(matchesAny(patterns, "/src/main.js"), false);
  assert.equal(matchesAny(patterns, "/resources/logo.svg"), false);
});

test("stageDaemonForPackage forwards platform and arch to the daemon bundler", async () => {
  const calls = [];
  const result = await stageDaemonForPackage({
    platform: "linux",
    arch: "arm64",
    version: "1.2.3",
    bundleStdio: "pipe",
    bundleDaemon: (request) => {
      calls.push(request);
      return path.join(request.controlCenterRoot, "resources", "bin", "computehopd");
    }
  }, path.resolve("tmp", "control-center"), "linux");

  assert.equal(result, path.join(path.resolve("tmp", "control-center"), "resources", "bin", "computehopd"));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].platform, "linux");
  assert.equal(calls[0].targetOS, "linux");
  assert.equal(calls[0].targetArch, "arm64");
  assert.equal(calls[0].version, "1.2.3");
  assert.equal(calls[0].stdio, "pipe");
});

test("packageApp stages daemon before invoking packager", async () => {
  const events = [];
  const root = path.resolve("tmp", "control-center");

  const result = await packageApp({
    controlCenterRoot: root,
    out: path.join(root, "out"),
    platform: "darwin",
    arch: "arm64",
    bundleDaemon: () => {
      events.push("bundle");
      return path.join(root, "resources", "bin", "computehopd");
    },
    packager: async (options) => {
      events.push(["packager", options.platform, options.arch, options.extraResource[0]]);
      return [path.join(options.out, "ComputeHop Control Center-darwin-arm64")];
    }
  });

  assert.deepEqual(events, [
    "bundle",
    ["packager", "darwin", "arm64", path.join(root, "resources", "bin")]
  ]);
  assert.equal(result.daemonPath, path.join(root, "resources", "bin", "computehopd"));
  assert.deepEqual(result.appPaths, [path.join(root, "out", "ComputeHop Control Center-darwin-arm64")]);
});

function matchesAny(patterns, value) {
  return patterns.some((pattern) => pattern.test(value));
}
