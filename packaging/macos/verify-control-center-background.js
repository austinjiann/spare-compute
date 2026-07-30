#!/usr/bin/env node
"use strict";

const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const path = require("node:path");

async function verifyControlCenterBackground(appBundle, options = {}) {
  const bundle = path.resolve(String(appBundle || ""));
  if (!bundle || path.basename(bundle) !== "ComputeHop.app") {
    throw new Error("Usage: verify-control-center-background.js /path/to/ComputeHop.app");
  }

  const fsModule = options.fs || fs;
  const service = options.service || launchAgentService();
  const paths = controlCenterBackgroundPaths(bundle);

  await assertExecutable(paths.parentDaemon, fsModule, "parent app daemon");
  await assertExecutable(paths.controlCenterDaemon, fsModule, "nested Control Center daemon");

  const parentCandidate = service.parentAppDaemonPath(paths.controlCenterResources, "darwin");
  if (path.resolve(parentCandidate) !== paths.parentDaemon) {
    throw new Error(
      `Control Center parent daemon candidate is wrong: ${parentCandidate || "(empty)"}; expected ${paths.parentDaemon}`
    );
  }

  const resolved = await service.resolveDaemonExecutable({
    resourcesPath: paths.controlCenterResources
  }, fsModule, "darwin");

  if (path.resolve(resolved) === paths.controlCenterDaemon) {
    throw new Error("Control Center launch service resolves its nested daemon instead of the parent app daemon.");
  }
  if (path.resolve(resolved) !== paths.parentDaemon) {
    throw new Error(`Control Center launch service resolves ${resolved}; expected parent daemon ${paths.parentDaemon}`);
  }

  return {
    ok: true,
    parentDaemon: paths.parentDaemon,
    controlCenterDaemon: paths.controlCenterDaemon,
    resolvedDaemon: path.resolve(resolved)
  };
}

function launchAgentService() {
  const sourceTreeService = path.resolve(__dirname, "../../apps/control-center/src/launch-agent-service");
  if (fsSync.existsSync(`${sourceTreeService}.js`)) {
    return require(sourceTreeService);
  }
  return {
    parentAppDaemonPath,
    resolveDaemonExecutable
  };
}

async function resolveDaemonExecutable(options = {}, fsModule = fs, platform = process.platform) {
  const candidates = [
    parentAppDaemonPath(options.resourcesPath, platform),
    options.resourcesPath ? path.join(options.resourcesPath, "bin", executableName("computehopd", platform)) : ""
  ].filter(Boolean);

  for (const candidate of candidates) {
    if (await executableExists(candidate, fsModule, platform)) {
      return candidate;
    }
  }
  throw new Error("No bundled ComputeHop daemon was found.");
}

function parentAppDaemonPath(resourcesPath, platform) {
  if (!resourcesPath) {
    return "";
  }

  const currentResources = path.resolve(resourcesPath);
  const currentContents = path.dirname(currentResources);
  const currentApp = path.dirname(currentContents);
  if (path.basename(currentResources) !== "Resources" || path.basename(currentContents) !== "Contents" || !currentApp.endsWith(".app")) {
    return "";
  }

  const parentResources = path.dirname(currentApp);
  const parentContents = path.dirname(parentResources);
  const parentApp = path.dirname(parentContents);
  if (path.basename(parentResources) !== "Resources" || path.basename(parentContents) !== "Contents" || !parentApp.endsWith(".app")) {
    return "";
  }

  return path.join(parentResources, "bin", executableName("computehopd", platform));
}

async function executableExists(filePath, fsModule, platform) {
  try {
    const info = await fsModule.stat(filePath);
    return info.isFile() && (platform === "win32" || (info.mode & 0o111) !== 0);
  } catch {
    return false;
  }
}

function executableName(name, platform = process.platform) {
  return platform === "win32" ? `${name}.exe` : name;
}

function controlCenterBackgroundPaths(appBundle) {
  const bundle = path.resolve(appBundle);
  const resources = path.join(bundle, "Contents", "Resources");
  const controlCenterApp = path.join(resources, "ComputeHop Control Center.app");
  const controlCenterResources = path.join(controlCenterApp, "Contents", "Resources");

  return {
    parentDaemon: path.join(resources, "bin", "computehopd"),
    controlCenterApp,
    controlCenterResources,
    controlCenterDaemon: path.join(controlCenterResources, "bin", "computehopd")
  };
}

async function assertExecutable(filePath, fsModule, label) {
  try {
    const stat = await fsModule.stat(filePath);
    if (!stat.isFile() || (stat.mode & 0o111) === 0) {
      throw new Error(`${label} is not executable: ${filePath}`);
    }
  } catch (error) {
    if (error && error.code === "ENOENT") {
      throw new Error(`${label} is missing: ${filePath}`);
    }
    throw error;
  }
}

async function main(argv = process.argv) {
  try {
    const result = await verifyControlCenterBackground(argv[2]);
    console.log(`Verified Control Center background daemon path: ${result.resolvedDaemon}`);
  } catch (error) {
    console.error(error?.message || String(error));
    process.exitCode = 1;
  }
}

if (require.main === module) {
  void main();
}

module.exports = {
  controlCenterBackgroundPaths,
  verifyControlCenterBackground
};
