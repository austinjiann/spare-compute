const assert = require("node:assert/strict");
const test = require("node:test");
const {
  availabilityLabel,
  connectionPathLabel,
  deviceLabel,
  friendlyConnectionError,
  workerReadinessSummary
} = require("./device-status");

test("availabilityLabel does not mark offline trusted workers as nearby", () => {
  assert.equal(
    availabilityLabel({
      id: "worker-1",
      role: "worker",
      trustState: "paired",
      connection: "not connected",
      availability: "offline"
    }),
    "Offline"
  );

  assert.equal(
    availabilityLabel({
      id: "presence-1",
      role: "worker",
      trustState: "unpaired",
      connection: "not connected",
      availability: "nearby"
    }),
    "Nearby"
  );
});

test("deviceLabel describes connected paths without exposing route jargon", () => {
  assert.equal(
    deviceLabel({
      id: "worker-1",
      name: "Gaming PC",
      role: "worker",
      trustState: "paired",
      connection: "active",
      availability: "remote",
      path: "lan"
    }),
    "Computer · connected over LAN"
  );

  assert.equal(
    deviceLabel({
      id: "worker-2",
      name: "Home Server",
      role: "worker",
      trustState: "paired",
      connection: "active",
      availability: "remote",
      path: "turn-relay"
    }),
    "Server · connected over relay"
  );
});

test("friendlyConnectionError summarizes actionable offline reasons", () => {
  assert.equal(connectionPathLabel("ice-direct"), "direct link");
  assert.equal(friendlyConnectionError("re-pair this device to enable remote connectivity"), "needs reconnect setup");
  assert.equal(friendlyConnectionError("remote connectivity is disabled"), "remote access off");
  assert.equal(friendlyConnectionError("dial timeout"), "connection timed out");
});

test("workerReadinessSummary reports daemon and discovery blockers", () => {
  assert.deepEqual(
    workerReadinessSummary({ daemonAvailable: false }),
    {
      kind: "daemon-off",
      title: "ComputeHop is off",
      detail: "Start it to find workers and run tasks.",
      actionLabel: "Start",
      actionKind: "start-daemon",
      deviceID: ""
    }
  );

  assert.deepEqual(
    workerReadinessSummary({ lanDiscovery: false }),
    {
      kind: "discovery-off",
      title: "Nearby discovery off",
      detail: "Turn on discovery to find workers on this network.",
      actionLabel: "Turn on",
      actionKind: "enable-discovery",
      deviceID: ""
    }
  );
});

test("workerReadinessSummary reports ready workers", () => {
  const worker = connectedWorker("Austin MacBook 2", "worker-1");

  assert.deepEqual(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice(), worker],
      selectedDeviceID: "local"
    }),
    {
      kind: "ready",
      title: "Worker ready",
      detail: "Austin MacBook 2 can run tasks.",
      actionLabel: "Test",
      actionKind: "test-worker",
      deviceID: "worker-1"
    }
  );

  assert.equal(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice(), connectedWorker("Gaming PC", "worker-2")],
      selectedDeviceID: "worker-2"
    }).deviceID,
    ""
  );

  assert.equal(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice(), worker, connectedWorker("Gaming PC", "worker-2")]
    }).kind,
    "choose-worker"
  );
});

test("workerReadinessSummary reports disabled workers", () => {
  assert.deepEqual(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice(), { ...connectedWorker("Austin MacBook 2", "worker-1"), synced: false }]
    }),
    {
      kind: "disabled",
      title: "Worker paused",
      detail: "Austin MacBook 2 is disabled for tasks.",
      actionLabel: "Enable",
      actionKind: "enable-device",
      deviceID: "worker-1"
    }
  );

  assert.equal(
    workerReadinessSummary({
      daemonAvailable: true,
      selectedDeviceID: "worker-2",
      devices: [
        localDevice(),
        { ...connectedWorker("Austin MacBook 2", "worker-1"), synced: false },
        { ...connectedWorker("Gaming PC", "worker-2"), synced: false }
      ]
    }).deviceID,
    ""
  );

  assert.equal(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [
        localDevice(),
        { ...connectedWorker("Austin MacBook 2", "worker-1"), synced: false },
        { ...connectedWorker("Gaming PC", "worker-2"), synced: false }
      ]
    }).kind,
    "disabled"
  );
});

test("workerReadinessSummary reports pairing and nearby workers", () => {
  assert.deepEqual(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice()],
      pairings: [{
        peerName: "Gaming PC",
        state: "waiting",
        localConfirmed: false
      }]
    }),
    {
      kind: "pairing",
      title: "Pairing waiting",
      detail: "Confirm the code for Gaming PC below.",
      actionLabel: "",
      actionKind: "",
      deviceID: ""
    }
  );

  assert.deepEqual(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice(), nearbyWorker("Home Server", "presence-1")]
    }),
    {
      kind: "nearby",
      title: "Worker nearby",
      detail: "Connect to Home Server.",
      actionLabel: "Connect",
      actionKind: "connect-device",
      deviceID: "presence-1"
    }
  );
});

test("workerReadinessSummary reports offline and empty worker states", () => {
  assert.equal(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice(), { ...connectedWorker("Gaming PC", "worker-1"), availability: "offline", connection: "not connected" }]
    }).kind,
    "offline"
  );

  assert.equal(
    workerReadinessSummary({
      daemonAvailable: true,
      devices: [localDevice()]
    }).kind,
    "none"
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
    trustState: "paired",
    connection: "active",
    availability: "remote"
  };
}

function nearbyWorker(name, id) {
  return {
    id,
    name,
    role: "worker",
    trustState: "unpaired",
    connection: "not connected",
    availability: "nearby"
  };
}
