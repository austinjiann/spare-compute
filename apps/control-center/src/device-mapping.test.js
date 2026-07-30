const assert = require("node:assert/strict");
const test = require("node:test");
const {
  mapDevices,
  mapLocalDevice,
  mapPairing,
  timestampLabel
} = require("./device-mapping");

test("mapDevices merges a paired trusted worker with its nearby LAN sighting", () => {
  const result = mapDevices({
    trustedDevices: [
      trustedWorker({
        deviceId: "durable-worker-id",
        name: "Gaming PC",
        connectivityState: "CONNECTIVITY_STATE_DISABLED"
      })
    ],
    devices: [
      nearbyWorker({
        presenceId: "nearby-presence-id",
        name: "Gaming PC",
        addresses: ["192.0.2.20"],
        port: 47823,
        platform: "windows",
        arch: "amd64",
        lastSeenAtUnixNano: 1_800_000_000
      })
    ]
  });

  assert.equal(result.length, 1);
  assert.equal(result[0].id, "durable-worker-id");
  assert.equal(result[0].connection, "active");
  assert.equal(result[0].availability, "nearby");
  assert.equal(result[0].path, "lan");
  assert.equal(result[0].platform, "windows");
  assert.equal(result[0].arch, "amd64");
  assert.equal(result[0].address, "192.0.2.20:47823");
  assert.equal(result[0].updated, "1970-01-01T00:00:01.800Z");
});

test("mapDevices does not merge nearby rows when trusted names are ambiguous", () => {
  const result = mapDevices({
    trustedDevices: [
      trustedWorker({ deviceId: "worker-1", name: "Gaming PC" }),
      trustedWorker({ deviceId: "worker-2", name: "Gaming PC" })
    ],
    devices: [
      nearbyWorker({ presenceId: "presence-1", name: "Gaming PC" })
    ]
  });

  assert.deepEqual(result.map((device) => device.id), ["worker-1", "worker-2", "presence-1"]);
  assert.equal(result[0].availability, "offline");
  assert.equal(result[2].trustState, "unpaired");
});

test("mapDevices formats nearby addresses without corrupting IPv6", () => {
  const result = mapDevices({
    devices: [
      nearbyWorker({
        presenceId: "presence-v6",
        name: "Home Server",
        addresses: ["2001:db8::20"],
        port: 47823
      })
    ]
  });

  assert.equal(result[0].address, "[2001:db8::20]:47823");
});

test("mapLocalDevice labels this process platform and architecture", () => {
  const local = mapLocalDevice({
    deviceName: "Austin MacBook",
    deviceId: "durable-local-id",
    role: "DEVICE_ROLE_ORCHESTRATOR"
  });

  assert.equal(local.id, "local");
  assert.equal(local.deviceID, "durable-local-id");
  assert.equal(local.role, "orchestrator");
  assert.equal(local.platform, process.platform);
  assert.equal(local.arch, process.arch);
});

test("mapPairing normalizes pairing state and timestamps", () => {
  assert.deepEqual(
    mapPairing({
      id: "pairing-id",
      peerDeviceId: "worker-id",
      peerName: "Worker",
      peerRole: "DEVICE_ROLE_WORKER",
      verificationCode: "1234-5678",
      direction: "PAIRING_DIRECTION_INBOUND",
      state: "PAIRING_STATE_WAITING",
      localConfirmed: true,
      remoteConfirmed: false,
      expiresAtUnixNano: 2_500_000_000
    }),
    {
      id: "pairing-id",
      peerDeviceID: "worker-id",
      peerName: "Worker",
      peerRole: "worker",
      verificationCode: "1234-5678",
      direction: "inbound",
      state: "waiting",
      localConfirmed: true,
      remoteConfirmed: false,
      expiresAt: "1970-01-01T00:00:02.500Z",
      failure: ""
    }
  );
  assert.equal(timestampLabel(0), "");
});

function trustedWorker(overrides = {}) {
  return {
    pairId: "pair-id",
    deviceId: "worker-id",
    name: "Worker",
    role: "DEVICE_ROLE_WORKER",
    trustState: "DEVICE_TRUST_STATE_PAIRED",
    connectivityState: "CONNECTIVITY_STATE_UNAVAILABLE",
    updatedAtUnixNano: 1_000_000_000,
    ...overrides
  };
}

function nearbyWorker(overrides = {}) {
  return {
    presenceId: "presence-id",
    name: "Worker",
    role: "DEVICE_ROLE_WORKER",
    endpointReady: true,
    trustState: "DEVICE_TRUST_STATE_UNPAIRED",
    addresses: ["192.0.2.10"],
    port: 47823,
    lastSeenAtUnixNano: 1_500_000_000,
    ...overrides
  };
}
