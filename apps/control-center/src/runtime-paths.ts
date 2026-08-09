const path = require("node:path");

function controlCenterRootForModule(moduleDirectory: string): string {
  const parent = path.dirname(moduleDirectory);
  return path.basename(parent) === "dist" ? path.dirname(parent) : parent;
}

function runtimeRootForModule(moduleDirectory: string): string {
  const parent = path.dirname(moduleDirectory);
  return path.basename(parent) === "dist" ? parent : controlCenterRootForModule(moduleDirectory);
}

module.exports = {
  controlCenterRootForModule,
  runtimeRootForModule
};
