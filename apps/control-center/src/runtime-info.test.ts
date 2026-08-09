const assert = require("node:assert/strict");
const test = require("node:test");
const {
  appRuntimeInfo,
  daemonRolesForPlatform,
  normalizeDaemonRole
} = require("./runtime-info");

test("macOS can start either Control Mac or Worker", () => {
  assert.deepEqual(daemonRolesForPlatform("darwin"), [
    { id: "orchestrator", label: "Control Mac" },
    { id: "worker", label: "Worker" }
  ]);
  assert.equal(appRuntimeInfo("darwin").defaultDaemonRole, "orchestrator");
  assert.equal(normalizeDaemonRole("worker", "darwin"), "worker");
});

test("non-macOS platforms start as workers only", () => {
  assert.deepEqual(daemonRolesForPlatform("win32"), [
    { id: "worker", label: "Worker" }
  ]);
  assert.deepEqual(daemonRolesForPlatform("linux"), [
    { id: "worker", label: "Worker" }
  ]);
  assert.equal(appRuntimeInfo("win32").defaultDaemonRole, "worker");
  assert.equal(normalizeDaemonRole("orchestrator", "win32"), "worker");
  assert.equal(normalizeDaemonRole("", "linux"), "worker");
});
