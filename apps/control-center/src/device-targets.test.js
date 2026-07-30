const assert = require("node:assert/strict");
const test = require("node:test");
const {
  addAutomaticWorkerTarget,
  automaticWorkerID,
  bestWorkerFromCandidates,
  concreteDeviceID,
  deviceHasRequiredTools,
  isSingleAutoCandidate,
  missingToolIDsForPlan,
  requiredToolIDsForPlan,
  singleConnectedWorkerTarget,
  compatibleWorkerForPlan,
  workerResourceScore,
  workerMatchesArchitecture,
  workerMatchesPlatform,
  workerRunTargetForAction,
  workerTargetAfterPairingConfirmation
} = require("./device-targets");

test("addAutomaticWorkerTarget inserts Auto worker for exactly one connected worker", () => {
  const result = addAutomaticWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1")
  ], "local");

  assert.deepEqual(result.devices.map((device) => device.id), ["local", automaticWorkerID, "worker-1"]);
  assert.equal(result.devices[1].name, "Auto worker");
  assert.equal(result.devices[1].detail, "Uses Austin MacBook 2");
  assert.equal(result.devices[1].workerID, "worker-1");
  assert.equal(result.devices[1].workerName, "Austin MacBook 2");
  assert.equal(result.selectedDeviceID, "local");
});

test("addAutomaticWorkerTarget can default to Auto worker for first-run offload", () => {
  const result = addAutomaticWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1")
  ], "local", { preferAutomaticWorker: true });

  assert.equal(result.selectedDeviceID, automaticWorkerID);
});

test("addAutomaticWorkerTarget preserves explicit worker selection over Auto preference", () => {
  const result = addAutomaticWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1")
  ], "worker-1", { preferAutomaticWorker: true });

  assert.equal(result.selectedDeviceID, "worker-1");
});

test("addAutomaticWorkerTarget preserves Auto worker selection while valid", () => {
  const result = addAutomaticWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1")
  ], automaticWorkerID);

  assert.equal(result.selectedDeviceID, automaticWorkerID);
});

test("addAutomaticWorkerTarget can keep a selected worker pending while unavailable", () => {
  const result = addAutomaticWorkerTarget([
    localDevice()
  ], "worker-1", { preserveUnavailableSelection: true });

  assert.deepEqual(result.devices.map((device) => device.id), ["local"]);
  assert.equal(result.selectedDeviceID, "worker-1");
});

test("addAutomaticWorkerTarget can keep Auto worker pending while unavailable", () => {
  const result = addAutomaticWorkerTarget([
    localDevice()
  ], automaticWorkerID, { preserveUnavailableSelection: true });

  assert.deepEqual(result.devices.map((device) => device.id), ["local"]);
  assert.equal(result.selectedDeviceID, automaticWorkerID);
});

test("addAutomaticWorkerTarget removes Auto worker when selection would be ambiguous", () => {
  const result = addAutomaticWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1"),
    connectedWorker("Gaming PC", "worker-2")
  ], automaticWorkerID);

  assert.deepEqual(result.devices.map((device) => device.id), ["local", "worker-1", "worker-2"]);
  assert.equal(result.selectedDeviceID, "local");
});

test("addAutomaticWorkerTarget ignores offline, unpaired, local, and non-worker devices", () => {
  const result = addAutomaticWorkerTarget([
    localDevice(),
    { ...connectedWorker("Offline worker", "worker-1"), availability: "offline" },
    { ...connectedWorker("Nearby worker", "worker-2"), connection: "not connected", availability: "nearby" },
    { ...connectedWorker("Control Mac", "control-1"), role: "orchestrator" }
  ], "local");

  assert.deepEqual(result.devices.map((device) => device.id), ["local", "worker-1", "worker-2", "control-1"]);
});

test("isSingleAutoCandidate accepts only active connected workers", () => {
  assert.equal(isSingleAutoCandidate(connectedWorker("Worker", "worker-1")), true);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), availability: "nearby" }), true);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Auto worker", automaticWorkerID), automatic: true }), false);
  assert.equal(isSingleAutoCandidate(localDevice()), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), role: "orchestrator" }), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), availability: "offline" }), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), connection: "not connected" }), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), synced: false }), false);
});

test("compatibleWorkerForPlan selects one matching worker by platform", () => {
  const mac = { ...connectedWorker("Mac mini", "worker-1"), platform: "darwin" };
  const windows = { ...connectedWorker("Gaming PC", "worker-2"), platform: "windows" };
  const linux = { ...connectedWorker("Home Server", "worker-3"), platform: "linux" };
  const strongerWindows = { ...windows, id: "worker-4", logicalCPUCount: 32, totalMemoryBytes: 64 * 1024 ** 3 };

  assert.equal(compatibleWorkerForPlan([localDevice(), mac, windows, linux], { targetPlatform: "windows" }).id, "worker-2");
  assert.equal(compatibleWorkerForPlan([localDevice(), mac, windows, linux], { targetPlatform: "macos" }).id, "worker-1");
  assert.equal(compatibleWorkerForPlan([localDevice(), mac, windows, linux], { requiredPlatform: "linux" }).id, "worker-3");
  assert.equal(compatibleWorkerForPlan([localDevice(), mac, windows, { ...windows, id: "worker-4" }], { targetPlatform: "windows" }).id, "worker-2");
  assert.equal(compatibleWorkerForPlan([localDevice(), mac, windows, strongerWindows], { targetPlatform: "windows" }).id, "worker-4");
  assert.equal(compatibleWorkerForPlan([localDevice(), mac, windows], { targetPlatform: "" }), null);
  assert.equal(workerMatchesPlatform({ platform: "win32" }, "windows"), true);
  assert.equal(workerMatchesPlatform({ platform: "darwin" }, "linux"), false);
});

test("compatibleWorkerForPlan can select by allowed-work policy", () => {
  const buildWorker = { ...connectedWorker("Build Mac", "worker-1"), platform: "darwin" };
  const dockerWorker = { ...connectedWorker("Docker PC", "worker-2"), platform: "windows" };

  assert.equal(
    compatibleWorkerForPlan([localDevice(), buildWorker, dockerWorker], { command: "docker build ." }, {
      requireAllowedMatch: true,
      isWorkerAllowed: (device) => device.id === "worker-2"
    }).id,
    "worker-2"
  );
  assert.equal(
    compatibleWorkerForPlan([localDevice(), buildWorker, dockerWorker], { targetPlatform: "windows" }, {
      isWorkerAllowed: (device) => device.id === "worker-1"
    }),
    null
  );
  assert.equal(
    compatibleWorkerForPlan([localDevice(), buildWorker, dockerWorker], { command: "docker build ." }, {
      requireAllowedMatch: true,
      isWorkerAllowed: () => true
    }).id,
    "worker-1"
  );
  assert.equal(
    compatibleWorkerForPlan([
      localDevice(),
      { ...buildWorker, logicalCPUCount: 8, totalMemoryBytes: 16 * 1024 ** 3 },
      { ...dockerWorker, logicalCPUCount: 24, totalMemoryBytes: 64 * 1024 ** 3 }
    ], { command: "docker build ." }, {
      requireAllowedMatch: true,
      isWorkerAllowed: () => true
    }).id,
    "worker-2"
  );
});

test("compatibleWorkerForPlan prefers workers that report required tools", () => {
  const goWorker = { ...connectedWorker("Build Mac", "worker-1"), toolIDs: ["go", "make"] };
  const dockerWorker = { ...connectedWorker("Docker PC", "worker-2"), toolIDs: ["docker", "node"] };
  const oldWorker = { ...connectedWorker("Old worker", "worker-3"), toolIDs: [] };

  assert.equal(
    compatibleWorkerForPlan([localDevice(), goWorker, dockerWorker], { command: "docker build .", targetPreference: "worker" }).id,
    "worker-2"
  );
  assert.equal(
    compatibleWorkerForPlan([localDevice(), dockerWorker], { command: "go test ./...", targetPreference: "worker" }),
    null
  );
  assert.equal(
    compatibleWorkerForPlan([localDevice(), oldWorker], { command: "go test ./...", targetPreference: "worker" }).id,
    "worker-3"
  );
});

test("tool matching derives command executables and ignores unknown old hints", () => {
  assert.deepEqual(requiredToolIDsForPlan({ command: "go test ./..." }), ["go"]);
  assert.deepEqual(requiredToolIDsForPlan({ command: "docker compose build" }), ["docker"]);
  assert.deepEqual(requiredToolIDsForPlan({ command: "/bin/hostname" }), ["hostname"]);
  assert.deepEqual(requiredToolIDsForPlan({ command: "./scripts/check" }), []);
  assert.deepEqual(requiredToolIDsForPlan({ requiredTools: [" Docker ", "go", "go"] }), ["docker", "go"]);

  assert.equal(deviceHasRequiredTools({ toolIDs: ["go"] }, { command: "go test ./..." }), true);
  assert.equal(deviceHasRequiredTools({ toolIDs: ["node"] }, { command: "go test ./..." }), false);
  assert.equal(deviceHasRequiredTools({ toolIDs: [] }, { command: "go test ./..." }), true);
  assert.deepEqual(missingToolIDsForPlan({ toolIDs: ["node"] }, { command: "go test ./..." }), ["go"]);
});

test("compatibleWorkerForPlan picks the strongest worker for worker-targeted tasks", () => {
  const weak = { ...connectedWorker("Small Mac", "worker-1"), logicalCPUCount: 8, totalMemoryBytes: 16 * 1024 ** 3 };
  const strong = { ...connectedWorker("Gaming PC", "worker-2"), logicalCPUCount: 32, totalMemoryBytes: 64 * 1024 ** 3 };

  assert.equal(
    compatibleWorkerForPlan([localDevice(), weak, strong], { command: "hostname", targetPreference: "worker" }).id,
    "worker-2"
  );
});

test("bestWorkerFromCandidates uses resources then stable IDs", () => {
  const left = { ...connectedWorker("Left", "worker-1"), logicalCPUCount: 8, totalMemoryBytes: 16 * 1024 ** 3 };
  const right = { ...connectedWorker("Right", "worker-2"), logicalCPUCount: 8, totalMemoryBytes: 16 * 1024 ** 3 };
  const stronger = { ...connectedWorker("Strong", "worker-3"), logicalCPUCount: 8, totalMemoryBytes: 32 * 1024 ** 3 };

  assert.equal(bestWorkerFromCandidates([right, left]).id, "worker-1");
  assert.equal(bestWorkerFromCandidates([left, stronger]).id, "worker-3");
  assert.equal(workerResourceScore(stronger) > workerResourceScore(left), true);
});

test("compatibleWorkerForPlan selects one matching worker by architecture", () => {
  const appleSilicon = { ...connectedWorker("M-series Mac", "worker-1"), platform: "darwin", arch: "arm64" };
  const intel = { ...connectedWorker("Intel Mac", "worker-2"), platform: "darwin", arch: "amd64" };
  const windows = { ...connectedWorker("Windows PC", "worker-3"), platform: "windows", arch: "x64" };

  assert.equal(compatibleWorkerForPlan([localDevice(), appleSilicon, intel, windows], { targetArchitecture: "arm64" }).id, "worker-1");
  assert.equal(compatibleWorkerForPlan([localDevice(), appleSilicon, intel, windows], { targetPlatform: "darwin", targetArchitecture: "x64" }).id, "worker-2");
  assert.equal(compatibleWorkerForPlan([localDevice(), appleSilicon, intel, windows], { requiredArchitecture: "amd64" }).id, "worker-2");
  assert.equal(workerMatchesArchitecture({ arch: "aarch64" }, "arm64"), true);
  assert.equal(workerMatchesArchitecture({ arch: "x64" }, "amd64"), true);
  assert.equal(workerMatchesArchitecture({ arch: "arm64" }, "amd64"), false);
});

test("concreteDeviceID resolves Auto worker to its backing worker", () => {
  assert.equal(concreteDeviceID({ id: automaticWorkerID, workerID: "worker-1" }), "worker-1");
  assert.equal(concreteDeviceID({ id: automaticWorkerID }), automaticWorkerID);
  assert.equal(concreteDeviceID(connectedWorker("Worker", "worker-1")), "worker-1");
  assert.equal(concreteDeviceID(null), "local");
});

test("singleConnectedWorkerTarget returns an automatic target only when unambiguous", () => {
  const single = singleConnectedWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1")
  ]);

  assert.equal(single.id, automaticWorkerID);
  assert.equal(single.workerID, "worker-1");
  assert.equal(single.workerName, "Austin MacBook 2");
  assert.equal(singleConnectedWorkerTarget([
    localDevice(),
    single,
    connectedWorker("Austin MacBook 2", "worker-1")
  ]).workerID, "worker-1");

  assert.equal(singleConnectedWorkerTarget([
    localDevice(),
    connectedWorker("Austin MacBook 2", "worker-1"),
    connectedWorker("Gaming PC", "worker-2")
  ]), null);
  assert.equal(singleConnectedWorkerTarget([
    localDevice(),
    { ...connectedWorker("Offline worker", "worker-1"), availability: "offline" }
  ]), null);
});

test("workerRunTargetForAction prefers the Auto worker target for readiness actions", () => {
  const worker = connectedWorker("Austin MacBook 2", "worker-1");
  const withAuto = addAutomaticWorkerTarget([
    localDevice(),
    worker
  ], "local");

  const target = workerRunTargetForAction(withAuto.devices, "worker-1");

  assert.equal(target.id, automaticWorkerID);
  assert.equal(target.workerID, "worker-1");
  assert.equal(workerRunTargetForAction([localDevice(), worker], "worker-1").id, "worker-1");
  assert.equal(
    workerRunTargetForAction([
      localDevice(),
      { ...worker, synced: false }
    ], "worker-1"),
    null
  );
});

test("workerTargetAfterPairingConfirmation selects only unambiguous new worker targets", () => {
  const worker = connectedWorker("Austin MacBook 2", "worker-1");
  const otherWorker = connectedWorker("Gaming PC", "worker-2");
  const withAuto = addAutomaticWorkerTarget([
    localDevice(),
    worker
  ], "local");

  const target = workerTargetAfterPairingConfirmation(withAuto.devices, "local");

  assert.equal(target.id, automaticWorkerID);
  assert.equal(target.workerID, "worker-1");
  assert.equal(
    workerTargetAfterPairingConfirmation([localDevice(), worker, otherWorker], "local"),
    null
  );
  assert.equal(
    workerTargetAfterPairingConfirmation(withAuto.devices, "worker-1"),
    null
  );
  assert.equal(
    workerTargetAfterPairingConfirmation(withAuto.devices, "missing-worker").workerID,
    "worker-1"
  );
});

function localDevice() {
  return {
    id: "local",
    name: "This Mac",
    role: "orchestrator",
    connection: "active",
    availability: "local"
  };
}

function connectedWorker(name, id) {
  return {
    id,
    name,
    role: "worker",
    connection: "active",
    availability: "remote",
    trustState: "paired"
  };
}
