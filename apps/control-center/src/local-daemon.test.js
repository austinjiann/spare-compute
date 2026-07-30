const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const protobuf = require("protobufjs");
const { LocalDaemonClient } = require("./local-daemon");

const protoPath = path.resolve(
  __dirname,
  "..",
  "..",
  "..",
  "api",
  "proto",
  "computehop",
  "local",
  "v1",
  "local.proto"
);

test("LocalDaemonClient sends authenticated framed protobuf requests", async (t) => {
  if (process.platform === "win32") {
    t.skip("test uses Unix sockets");
  }

  const stateDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-control-center-"));
  await fs.chmod(stateDirectory, 0o700);
  t.after(async () => {
    await fs.rm(stateDirectory, { recursive: true, force: true });
  });

  const token = Buffer.alloc(32, 7);
  await fs.writeFile(path.join(stateDirectory, "local-ipc.token"), token.toString("base64").replace(/=+$/, "") + "\n", {
    mode: 0o600
  });

  const root = await protobuf.load(protoPath);
  const Request = root.lookupType("computehop.local.v1.Request");
  const Response = root.lookupType("computehop.local.v1.Response");
  const socketPath = path.join(stateDirectory, "computehop.sock");
  const received = [];

  const server = net.createServer((socket) => {
    let buffered = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      buffered = Buffer.concat([buffered, chunk]);
      if (buffered.length < 4) {
        return;
      }
      const length = buffered.readUInt32BE(0);
      if (buffered.length < length + 4) {
        return;
      }
      const request = Request.decode(buffered.subarray(4, length + 4));
      received.push(request);
      const response = Response.encode(
        Response.create({
          protocolVersion: 6,
          requestId: request.requestId,
          ping: {
            daemonVersion: "test",
            deviceId: "device-1",
            deviceName: "Test Mac",
            role: 2
          }
        })
      ).finish();
      const header = Buffer.alloc(4);
      header.writeUInt32BE(response.length, 0);
      socket.end(Buffer.concat([header, response]));
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });
  t.after(() => {
    server.close();
  });

  const client = new LocalDaemonClient({ stateDirectory });
  const ping = await client.ping();

  assert.equal(ping.daemonVersion, "test");
  assert.equal(ping.deviceId, "device-1");
  assert.equal(ping.deviceName, "Test Mac");
  assert.equal(ping.role, "DEVICE_ROLE_ORCHESTRATOR");
  assert.equal(received.length, 1);
  assert.equal(received[0].protocolVersion, 6);
  assert.equal(Buffer.compare(Buffer.from(received[0].capabilityToken), token), 0);
});

test("LocalDaemonClient sends pairing and unpair operations", async (t) => {
  if (process.platform === "win32") {
    t.skip("test uses Unix sockets");
  }

  const stateDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-control-center-"));
  await fs.chmod(stateDirectory, 0o700);
  t.after(async () => {
    await fs.rm(stateDirectory, { recursive: true, force: true });
  });

  const token = Buffer.alloc(32, 9);
  await fs.writeFile(path.join(stateDirectory, "local-ipc.token"), token.toString("base64").replace(/=+$/, "") + "\n", {
    mode: 0o600
  });

  const root = await protobuf.load(protoPath);
  const Request = root.lookupType("computehop.local.v1.Request");
  const Response = root.lookupType("computehop.local.v1.Response");
  const socketPath = path.join(stateDirectory, "computehop.sock");
  const received = [];

  const server = net.createServer((socket) => {
    let buffered = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      buffered = Buffer.concat([buffered, chunk]);
      if (buffered.length < 4) {
        return;
      }
      const length = buffered.readUInt32BE(0);
      if (buffered.length < length + 4) {
        return;
      }
      const request = Request.decode(buffered.subarray(4, length + 4));
      received.push(request);
      const response = Response.encode(Response.create(pairingResponse(request))).finish();
      const header = Buffer.alloc(4);
      header.writeUInt32BE(response.length, 0);
      socket.end(Buffer.concat([header, response]));
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });
  t.after(() => {
    server.close();
  });

  const client = new LocalDaemonClient({ stateDirectory });

  const pairings = await client.listPairings();
  const begun = await client.beginPairing("worker-1");
  const confirmed = await client.confirmPairing("pair-1");
  const rejected = await client.rejectPairing("pair-1");
  const forgotten = await client.unpairDevice("worker-1");

  assert.equal(pairings[0].peerName, "Worker");
  assert.equal(begun.id, "pair-1");
  assert.equal(confirmed.localConfirmed, true);
  assert.equal(rejected.state, "PAIRING_STATE_REJECTED");
  assert.equal(forgotten.deviceId, "worker-1");
  assert.equal(received[0].listPairings != null, true);
  assert.equal(received[1].beginPairing.deviceSelector, "worker-1");
  assert.equal(received[2].confirmPairing.pairingSelector, "pair-1");
  assert.equal(received[3].rejectPairing.pairingSelector, "pair-1");
  assert.equal(received[4].unpairDevice.deviceSelector, "worker-1");
});

function pairingResponse(request) {
  const envelope = {
    protocolVersion: 6,
    requestId: request.requestId
  };
  const pairing = {
    id: "pair-1",
    peerDeviceId: "worker-1",
    peerName: "Worker",
    peerRole: 1,
    verificationCode: "ABCD-EFGH",
    direction: 1,
    state: 1,
    localConfirmed: false,
    remoteConfirmed: false
  };

  if (request.listPairings) {
    return { ...envelope, listPairings: { pairings: [pairing] } };
  }
  if (request.beginPairing) {
    return { ...envelope, beginPairing: { pairing } };
  }
  if (request.confirmPairing) {
    return { ...envelope, confirmPairing: { pairing: { ...pairing, localConfirmed: true } } };
  }
  if (request.rejectPairing) {
    return { ...envelope, rejectPairing: { pairing: { ...pairing, state: 3 } } };
  }
  if (request.unpairDevice) {
    return {
      ...envelope,
      unpairDevice: {
        device: {
          pairId: "pair-1",
          deviceId: "worker-1",
          name: "Worker",
          role: 1,
          trustState: 2
        }
      }
    };
  }
  return { ...envelope, error: { code: 1, message: "unexpected request" } };
}
