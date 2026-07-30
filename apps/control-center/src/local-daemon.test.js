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
