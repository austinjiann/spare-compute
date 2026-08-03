#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const controlCenterRoot = path.resolve(__dirname, "..");
const repositoryRoot = path.resolve(controlCenterRoot, "..", "..");

function bundleDaemon(options = {}) {
  const output = daemonOutputPath(options);
  fs.mkdirSync(path.dirname(output), { recursive: true });

  const result = runGoBuild(options, output);
  if (result.error) {
    throw new Error(`Could not build computehopd: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new Error(`Could not build computehopd: go exited with code ${result.status}`);
  }
  if ((options.platform || process.platform) !== "win32") {
    fs.chmodSync(output, 0o755);
  }
  return output;
}

function runGoBuild(options, output) {
  const runner = options.spawnSync || spawnSync;
  return runner(options.go || "go", goBuildArguments(options, output), {
    cwd: options.repositoryRoot || repositoryRoot,
    env: goBuildEnvironment(options),
    stdio: options.stdio || "inherit"
  });
}

function goBuildArguments(options = {}, output = daemonOutputPath(options)) {
  const version = releaseVersion(options);
  return [
    "build",
    "-trimpath",
    "-ldflags",
    `-s -w -X main.version=${version}`,
    "-o",
    output,
    "./cmd/computehopd"
  ];
}

function releaseVersion(options = {}) {
  if (options.version) {
    return options.version;
  }
  if (process.env.COMPUTEHOP_VERSION) {
    return process.env.COMPUTEHOP_VERSION;
  }
  return fs.readFileSync(path.join(options.repositoryRoot || repositoryRoot, "VERSION"), "utf8").trim();
}

function goBuildEnvironment(options = {}) {
  const env = { ...process.env };
  if (options.targetOS) {
    env.GOOS = options.targetOS;
  }
  if (options.targetArch) {
    env.GOARCH = options.targetArch;
  }
  return env;
}

function daemonOutputPath(options = {}) {
  const root = options.controlCenterRoot || controlCenterRoot;
  const platform = options.platform || options.targetOS || process.platform;
  return path.join(root, "resources", "bin", executableName("computehopd", platform));
}

function executableName(name, platform = process.platform) {
  return platform === "win32" ? `${name}.exe` : name;
}

if (require.main === module) {
  try {
    const output = bundleDaemon();
    console.log(`Bundled ${output}`);
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
}

module.exports = {
  bundleDaemon,
  daemonOutputPath,
  executableName,
  goBuildArguments,
  goBuildEnvironment,
  releaseVersion
};
