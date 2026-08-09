#!/usr/bin/env node
const path = require("node:path");
const { bundleDaemon } = require("./bundle-daemon");
const { controlCenterRootForModule } = require("../src/runtime-paths");

const controlCenterRoot = controlCenterRootForModule(__dirname);

async function packageApp(options: any = {}) {
  const root = path.resolve(options.controlCenterRoot || controlCenterRoot);
  const packageOptions = createPackageOptions({ ...options, controlCenterRoot: root });
  const daemonPath = await stageDaemonForPackage(options, root, packageOptions.platform);
  const packager = options.packager || await loadPackager();
  const appPaths = await packager(packageOptions);

  return {
    appPaths,
    daemonPath,
    packageOptions
  };
}

function createPackageOptions(options: any = {}) {
  const root = path.resolve(options.controlCenterRoot || controlCenterRoot);
  const out = path.resolve(options.out || path.join(root, ".out"));
  const platform = options.platform || process.platform;
  const arch = options.arch || process.arch;
  const resourcesBin = path.join(root, "resources", "bin");

  return {
    dir: root,
    name: options.name || "ComputeHop Control Center",
    executableName: options.executableName || "ComputeHop",
    appBundleId: options.appBundleId || "com.computehop.controlcenter",
    appCategoryType: "public.app-category.developer-tools",
    out,
    overwrite: true,
    asar: true,
    prune: true,
    platform,
    arch,
    extraResource: [resourcesBin],
    ignore: packageIgnorePatterns()
  };
}

function packageIgnorePatterns() {
  return [
    /^\/\.out($|\/)/,
    /^\/out($|\/)/,
    /^\/resources\/bin($|\/)/,
    /^\/\.env(?:\..+)?$/,
    /^\/scripts($|\/)/,
    /^\/src($|\/)/,
    /^\/types($|\/)/,
    /^\/tsconfig(?:\..+)?\.json$/,
    /^\/dist\/scripts($|\/)/,
    /^\/dist\/support($|\/)/,
    /^\/dist\/.*\.js\.map$/,
    /^\/dist\/scripts\/.*\.test\.js$/,
    /^\/dist\/src\/.*\.test\.js$/
  ];
}

async function stageDaemonForPackage(options, root, platform) {
  if (options.bundleDaemon === false) {
    return "";
  }

  const bundler = options.bundleDaemon || bundleDaemon;
  return bundler({
    controlCenterRoot: root,
    platform,
    targetOS: options.targetOS || platform,
    targetArch: options.targetArch || options.arch,
    version: options.version,
    stdio: options.bundleStdio || "inherit"
  });
}

async function loadPackager() {
  const packagerModule = await import("@electron/packager");
  return packagerModule.packager;
}

if (require.main === module) {
  packageApp()
    .then((result) => {
      console.log(`Bundled daemon: ${result.daemonPath}`);
      for (const appPath of result.appPaths) {
        console.log(`Packaged app: ${appPath}`);
      }
    })
    .catch((error) => {
      console.error(error.message);
      process.exit(1);
    });
}

module.exports = {
  createPackageOptions,
  packageApp,
  packageIgnorePatterns,
  stageDaemonForPackage
};
