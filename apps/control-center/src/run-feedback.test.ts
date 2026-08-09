const assert = require("node:assert/strict");
const test = require("node:test");
const { friendlyRunError, remotePreparationMessage } = require("./run-feedback");

test("remotePreparationMessage describes remote project snapshot work", () => {
  assert.equal(
    remotePreparationMessage({
      deviceSelector: "5wc2jkni",
      deviceName: "Austin MacBook 2",
      workingDirectory: "/Users/austin/project"
    }),
    "Preparing remote run for Austin MacBook 2 from /Users/austin/project; snapshot/upload may take a moment."
  );
});

test("remotePreparationMessage falls back to selector when the display name is missing", () => {
  assert.equal(
    remotePreparationMessage({
      deviceSelector: "5wc2jkni",
      workingDirectory: "/project"
    }),
    "Preparing remote run for 5wc2jkni from /project; snapshot/upload may take a moment."
  );
});

test("remotePreparationMessage stays silent for local or projectless runs", () => {
  assert.equal(remotePreparationMessage({ deviceSelector: "", workingDirectory: "/project" }), "");
  assert.equal(remotePreparationMessage({ deviceSelector: "worker-1", workingDirectory: "" }), "");
});

test("friendlyRunError explains old-daemon offline worker submissions", () => {
  assert.equal(
    friendlyRunError({
      code: "ERROR_CODE_DEVICE_UNAVAILABLE",
      message: "paired worker is unavailable: Austin MacBook 2: remote connectivity path is unavailable"
    }),
    "Austin MacBook 2 is not reachable. Start ComputeHop on that computer and keep both computers on the same network, then try again. For different networks, set up VPS connectivity."
  );
});

test("friendlyRunError explains new-daemon offline worker submissions without leaking CLI commands", () => {
  const message = "paired worker is unavailable: Gaming PC is not reachable. " +
    "Start ComputeHop on that worker, put both devices on the same LAN, then run 'computehop smoke'. " +
    "For cross-network workers, run 'computehop setup vps'. Last error: remote connectivity path is unavailable";
  const result = friendlyRunError({
    code: "ERROR_CODE_DEVICE_UNAVAILABLE",
    message
  });
  assert.equal(
    result,
    "Gaming PC is not reachable. Start ComputeHop on that computer and keep both computers on the same network, then try again. For different networks, set up VPS connectivity."
  );
  assert.equal(result.includes("computehop"), false);
});

test("friendlyRunError gives a setup path when no worker is connected", () => {
  assert.equal(
    friendlyRunError({
      code: "ERROR_CODE_DEVICE_UNAVAILABLE",
      message: "paired worker is unavailable: no active paired worker is available for --on auto"
    }),
    "No connected worker is available. Start ComputeHop on the worker, connect it from Devices, then try again."
  );
});

test("friendlyRunError preserves unrelated errors", () => {
  assert.equal(
    friendlyRunError(new Error("Choose a project first.")),
    "Choose a project first."
  );
});
