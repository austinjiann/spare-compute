const assert = require("node:assert/strict");
const test = require("node:test");
const {
  addAutomaticWorkerTarget,
  automaticWorkerID,
  concreteDeviceID,
  isSingleAutoCandidate,
  singleConnectedWorkerTarget
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
  assert.equal(isSingleAutoCandidate(localDevice()), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), role: "orchestrator" }), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), availability: "offline" }), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), connection: "not connected" }), false);
  assert.equal(isSingleAutoCandidate({ ...connectedWorker("Worker", "worker-1"), synced: false }), false);
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
    connectedWorker("Austin MacBook 2", "worker-1"),
    connectedWorker("Gaming PC", "worker-2")
  ]), null);
  assert.equal(singleConnectedWorkerTarget([
    localDevice(),
    { ...connectedWorker("Offline worker", "worker-1"), availability: "offline" }
  ]), null);
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
