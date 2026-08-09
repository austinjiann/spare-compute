const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const protobuf = require("protobufjs");
const { splitCommandLine } = require("./command-line");
const { LocalDaemonClient } = require("./local-daemon");
const { jobStartRequestForPlan } = require("./run-request");

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

test("LocalDaemonClient sends job listing, logs, cancellation, and output fetch operations", async (t) => {
  if (process.platform === "win32") {
    t.skip("test uses Unix sockets");
  }

  const stateDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-control-center-"));
  await fs.chmod(stateDirectory, 0o700);
  t.after(async () => {
    await fs.rm(stateDirectory, { recursive: true, force: true });
  });

  const token = Buffer.alloc(32, 11);
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
      const response = Response.encode(Response.create(jobResponse(request))).finish();
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
  const project = path.join(stateDirectory, "project");
  await fs.mkdir(project);
  const planned = {
    plan: {
      command: "make macos-package",
      requiresProject: true,
      outputs: ["dist/macos/ComputeHop.app"],
      requiredToolIDs: ["make", "swift"]
    }
  };
  const jobRequest = jobStartRequestForPlan({
    plan: planned.plan,
    device: {
      id: "worker-1",
      name: "Gaming PC"
    },
    projectRoot: project,
    outputs: []
  });
  const argv = splitCommandLine(jobRequest.command);

  const submitted = await client.submitJob({
    executable: argv[0],
    arguments: argv.slice(1),
    workingDirectory: jobRequest.workingDirectory,
    outputs: jobRequest.outputs,
    requiredToolIDs: ["make", "swift"],
    deviceSelector: jobRequest.deviceID,
    jobID: "019abcdf-0123-4567-89ab-000000000333"
  });
  const jobs = await client.listJobs({ deviceSelector: "worker-1", limit: 3 });
  const progress = await client.getJobProgress("job-1", { deviceSelector: "worker-1" });
  const logs = await client.readJobLogs("job-1", { deviceSelector: "worker-1", afterSequence: 4, limit: 5 });
  const cancelled = await client.cancelJob("job-1", { deviceSelector: "worker-1" });
  const outputs = await client.fetchArtifacts("job-1", { deviceSelector: "worker-1", destination: "/tmp/out" });

  assert.equal(submitted.id, "job-1");
  assert.equal(jobs[0].id, "job-1");
  assert.equal(progress.phase, "JOB_PROGRESS_PHASE_UPLOAD");
  assert.equal(progress.completedBytes, 512);
  assert.equal(Buffer.from(logs.records[0].data).toString("utf8"), "hello\n");
  assert.equal(cancelled.state, "JOB_STATE_CANCELLED");
  assert.equal(outputs.destination, "/tmp/out");
  assert.equal(outputs.restoredFileCount, 2);
  assert.equal(received[0].submitJob.deviceSelector, "worker-1");
  assert.equal(received[0].submitJob.jobId, "019abcdf-0123-4567-89ab-000000000333");
  assert.equal(received[0].submitJob.spec.executable, "make");
  assert.deepEqual(received[0].submitJob.spec.arguments, ["macos-package"]);
  assert.equal(received[0].submitJob.spec.workingDirectory, project);
  assert.equal(received[0].submitJob.spec.executor, 1);
  assert.equal(received[0].submitJob.spec.containerImage, "");
  assert.deepEqual(received[0].submitJob.spec.outputs, ["dist/macos/ComputeHop.app"]);
  assert.deepEqual(received[0].submitJob.spec.requiredToolIds, ["make", "swift"]);
  assert.equal(received[1].listJobs.deviceSelector, "worker-1");
  assert.equal(received[1].listJobs.limit, 3);
  assert.equal(received[2].getJobProgress.jobId, "job-1");
  assert.equal(received[2].getJobProgress.deviceSelector, "worker-1");
  assert.equal(Number(received[3].readJobLogs.afterSequence), 4);
  assert.equal(received[3].readJobLogs.limit, 5);
  assert.equal(received[4].cancelJob.jobId, "job-1");
  assert.equal(received[5].fetchArtifacts.destination, "/tmp/out");
});

test("LocalDaemonClient can submit container executor requests", async (t) => {
  if (process.platform === "win32") {
    t.skip("test uses Unix sockets");
  }

  const stateDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-control-center-"));
  t.after(async () => {
    await fs.rm(stateDirectory, { recursive: true, force: true });
  });
  await fs.chmod(stateDirectory, 0o700);
  const token = Buffer.alloc(32, 9);
  await fs.writeFile(path.join(stateDirectory, "local-ipc.token"), token.toString("base64").replace(/=+$/, "") + "\n", {
    mode: 0o600
  });
  const received = [];
  const server = await startFakeDaemon(stateDirectory, (request) => {
    received.push(request);
    return jobResponse(request);
  });
  t.after(async () => {
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
  });

  const client = new LocalDaemonClient({ stateDirectory });
  await client.submitJob({
    executable: "echo",
    arguments: ["hello"],
    executor: "container",
    containerImage: "alpine:latest",
    deviceSelector: "worker-1"
  });

  assert.equal(received[0].submitJob.spec.executor, 2);
  assert.equal(received[0].submitJob.spec.containerImage, "alpine:latest");
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

function jobResponse(request) {
  const envelope = {
    protocolVersion: 6,
    requestId: request.requestId
  };
  const job = {
    id: "job-1",
    spec: {
      executable: "/bin/echo",
      arguments: ["hello"],
      executor: 1,
      outputs: ["dist"]
    },
    state: 10,
    createdAtUnixNano: 1,
    updatedAtUnixNano: 2
  };

  if (request.listJobs) {
    return { ...envelope, listJobs: { jobs: [job] } };
  }
  if (request.submitJob) {
    return {
      ...envelope,
      submitJob: {
        job: {
          ...job,
          spec: request.submitJob.spec
        }
      }
    };
  }
  if (request.getJobProgress) {
    return {
      ...envelope,
      getJobProgress: {
        progress: {
          phase: 2,
          completedBytes: 512,
          totalBytes: 1024,
          updatedAtUnixNano: 3
        }
      }
    };
  }
  if (request.readJobLogs) {
    return {
      ...envelope,
      readJobLogs: {
        job,
        records: [{ sequence: 5, stream: 1, data: Buffer.from("hello\n") }]
      }
    };
  }
  if (request.cancelJob) {
    return { ...envelope, cancelJob: { job: { ...job, state: 12 } } };
  }
  if (request.fetchArtifacts) {
    return {
      ...envelope,
      fetchArtifacts: {
        destination: request.fetchArtifacts.destination,
        restoredFileCount: 2,
        conflictFileCount: 1
      }
    };
  }
  return { ...envelope, error: { code: 1, message: "unexpected request" } };
}

async function startFakeDaemon(stateDirectory, responseFor) {
  const root = await protobuf.load(protoPath);
  const Request = root.lookupType("computehop.local.v1.Request");
  const Response = root.lookupType("computehop.local.v1.Response");
  const socketPath = path.join(stateDirectory, "computehop.sock");
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
      const response = Response.encode(Response.create(responseFor(request))).finish();
      const header = Buffer.alloc(4);
      header.writeUInt32BE(response.length, 0);
      socket.end(Buffer.concat([header, response]));
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });
  return server;
}
