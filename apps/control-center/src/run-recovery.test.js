const assert = require("node:assert/strict");
const test = require("node:test");
const {
  pendingPairingRunMatchesTarget,
  pendingPairingRunTarget,
  pendingRunAfterPairing,
  runControlCanRecover,
  workerTargetActionRequest
} = require("./run-recovery");

test("workerTargetActionRequest connects exactly one nearby worker", () => {
  assert.deepEqual(
    workerTargetActionRequest(
      { targetPreference: "worker" },
      [
        localDevice(),
        pairableWorker("worker-1", "Gaming PC")
      ]
    ),
    {
      workerTargetActionKind: "connect-device",
      workerTargetActionLabel: "Connect",
      workerTargetDeviceID: "worker-1"
    }
  );
});

test("workerTargetActionRequest stays silent for ambiguous or local plans", () => {
  assert.deepEqual(
    workerTargetActionRequest(
      { targetPreference: "worker" },
      [
        localDevice(),
        pairableWorker("worker-1", "Gaming PC"),
        pairableWorker("worker-2", "Mini PC")
      ]
    ),
    {}
  );
  assert.deepEqual(
    workerTargetActionRequest(
      { targetPreference: "local" },
      [
        localDevice(),
        pairableWorker("worker-1", "Gaming PC")
      ]
    ),
    {}
  );
});

test("runControlCanRecover allows project choice and safe nearby-worker connect", () => {
  assert.equal(
    runControlCanRecover({
      message: "Choose a project.",
      actionKind: "choose-project"
    }),
    true
  );
  assert.equal(
    runControlCanRecover(
      {
        message: "Connect a worker.",
        actionKind: "connect-device"
      },
      { actionDevice: pairableWorker("worker-1", "Gaming PC") }
    ),
    true
  );
  assert.equal(
    runControlCanRecover(
      {
        message: "Connect a worker.",
        actionKind: "connect-device"
      },
      { actionDevice: connectedWorker("worker-1", "Gaming PC") }
    ),
    false
  );
  assert.equal(
    runControlCanRecover({
      message: "Refresh first.",
      actionKind: "refresh"
    }),
    false
  );
});

test("pendingRunAfterPairing remembers only concrete remote workers", () => {
  const plan = {
    source: "run tests on the other computer",
    command: "go test ./..."
  };
  assert.deepEqual(
    pendingRunAfterPairing(plan, pairableWorker("worker-1", "Gaming PC")),
    {
      plan,
      workerID: "worker-1",
      task: "run tests on the other computer"
    }
  );
  assert.equal(pendingRunAfterPairing(plan, localDevice()), null);
});

test("pendingPairingRunTarget prefers the automatic target when the paired worker becomes runnable", () => {
  const pending = {
    workerID: "worker-1",
    plan: { command: "go test ./..." }
  };
  const automatic = {
    id: "auto",
    workerID: "worker-1",
    role: "worker",
    connection: "active",
    availability: "remote"
  };
  const target = pendingPairingRunTarget(pending, [
    localDevice(),
    connectedWorker("worker-1", "Gaming PC"),
    automatic
  ]);

  assert.equal(target, automatic);
  assert.equal(pendingPairingRunMatchesTarget(pending, target), true);
});

test("pendingPairingRunTarget ignores unavailable matching workers", () => {
  const pending = {
    workerID: "worker-1",
    plan: { command: "go test ./..." }
  };
  assert.equal(
    pendingPairingRunTarget(pending, [
      localDevice(),
      { ...connectedWorker("worker-1", "Gaming PC"), synced: false }
    ]),
    null
  );
});

function localDevice() {
  return {
    id: "local",
    name: "This Mac",
    connection: "active",
    availability: "local"
  };
}

function pairableWorker(id, name) {
  return {
    id,
    name,
    role: "worker",
    connection: "not connected",
    availability: "nearby"
  };
}

function connectedWorker(id, name) {
  return {
    id,
    name,
    role: "worker",
    connection: "active",
    availability: "nearby",
    trustState: "paired"
  };
}
