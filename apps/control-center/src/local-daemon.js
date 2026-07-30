const fs = require("node:fs/promises");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { randomUUID } = require("node:crypto");
const protobuf = require("protobufjs");

const PROTOCOL_VERSION = 6;
const MAX_FRAME_BYTES = 1 << 20;
const DEFAULT_TIMEOUT_MS = 15_000;
const SUBMIT_TIMEOUT_MS = 6 * 60 * 60 * 1000;
const LOCAL_PROTO_PATH = path.resolve(
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

const enumValues = {
  executorNative: 1,
  stdout: 1,
  stderr: 2
};

let schemaPromise;

class LocalDaemonError extends Error {
  constructor(message, code = "LOCAL_DAEMON_ERROR") {
    super(message);
    this.name = "LocalDaemonError";
    this.code = code;
  }
}

class LocalDaemonClient {
  constructor(options = {}) {
    this.stateDirectory = options.stateDirectory || defaultStateDirectory();
    this.socketPath = path.join(this.stateDirectory, "computehop.sock");
    this.tokenPath = path.join(this.stateDirectory, "local-ipc.token");
  }

  async ping(options = {}) {
    const response = await this.call({ ping: {} }, options);
    return response.ping || {};
  }

  async listDevices(options = {}) {
    const response = await this.call({ listDevices: {} }, options);
    return response.listDevices || {};
  }

  async listPairings(options = {}) {
    const response = await this.call({ listPairings: {} }, options);
    return response.listPairings?.pairings || [];
  }

  async beginPairing(deviceSelector, options = {}) {
    const response = await this.call(
      {
        beginPairing: {
          deviceSelector: deviceSelector || ""
        }
      },
      options
    );
    return response.beginPairing?.pairing || null;
  }

  async confirmPairing(pairingSelector, options = {}) {
    const response = await this.call(
      {
        confirmPairing: {
          pairingSelector: pairingSelector || ""
        }
      },
      options
    );
    return response.confirmPairing?.pairing || null;
  }

  async rejectPairing(pairingSelector, options = {}) {
    const response = await this.call(
      {
        rejectPairing: {
          pairingSelector: pairingSelector || ""
        }
      },
      options
    );
    return response.rejectPairing?.pairing || null;
  }

  async unpairDevice(deviceSelector, options = {}) {
    const response = await this.call(
      {
        unpairDevice: {
          deviceSelector: deviceSelector || ""
        }
      },
      options
    );
    return response.unpairDevice?.device || null;
  }

  async listJobs(options = {}) {
    const response = await this.call(
      {
        listJobs: {
          states: options.states || [],
          limit: options.limit || 20,
          deviceSelector: options.deviceSelector || ""
        }
      },
      options
    );
    return response.listJobs?.jobs || [];
  }

  async submitJob(jobRequest, options = {}) {
    const response = await this.call(
      {
        submitJob: {
          spec: {
            executable: jobRequest.executable,
            arguments: jobRequest.arguments || [],
            workingDirectory: jobRequest.workingDirectory || "",
            environment: {},
            executor: enumValues.executorNative,
            outputs: jobRequest.outputs || []
          },
          deviceSelector: jobRequest.deviceSelector || "",
          jobId: jobRequest.jobID || ""
        }
      },
      { timeoutMs: SUBMIT_TIMEOUT_MS, ...options }
    );
    return response.submitJob?.job || null;
  }

  async getJobProgress(jobID, options = {}) {
    const response = await this.call(
      {
        getJobProgress: {
          jobId: jobID,
          deviceSelector: options.deviceSelector || ""
        }
      },
      options
    );
    return response.getJobProgress?.progress || null;
  }

  async readJobLogs(jobID, options = {}) {
    const response = await this.call(
      {
        readJobLogs: {
          jobId: jobID,
          afterSequence: options.afterSequence || 0,
          limit: options.limit || 32,
          deviceSelector: options.deviceSelector || ""
        }
      },
      options
    );
    return response.readJobLogs || {};
  }

  async cancelJob(jobID, options = {}) {
    const response = await this.call(
      {
        cancelJob: {
          jobId: jobID,
          deviceSelector: options.deviceSelector || ""
        }
      },
      options
    );
    return response.cancelJob?.job || null;
  }

  async fetchArtifacts(jobID, options = {}) {
    const response = await this.call(
      {
        fetchArtifacts: {
          jobId: jobID,
          deviceSelector: options.deviceSelector || "",
          destination: options.destination || ""
        }
      },
      { timeoutMs: SUBMIT_TIMEOUT_MS, ...options }
    );
    return response.fetchArtifacts || {};
  }

  async call(operation, options = {}) {
    const { Request, Response } = await loadSchema();
    const requestID = randomUUID().toLowerCase();
    const token = await loadCapabilityToken(this.tokenPath);
    const request = Request.create({
      protocolVersion: PROTOCOL_VERSION,
      requestId: requestID,
      capabilityToken: token,
      ...operation
    });
    const validationError = Request.verify(request);
    if (validationError) {
      throw new LocalDaemonError(`Invalid local IPC request: ${validationError}`, "INVALID_REQUEST");
    }

    const payload = Request.encode(request).finish();
    const responsePayload = await sendFramedRequest(this.socketPath, payload, options);
    const response = Response.decode(responsePayload);
    const decoded = Response.toObject(response, {
      bytes: Buffer,
      defaults: false,
      enums: String,
      longs: Number
    });

    if (decoded.protocolVersion !== PROTOCOL_VERSION || decoded.requestId !== requestID) {
      throw new LocalDaemonError(
        "ComputeHop daemon does not match this Control Center. Restart ComputeHop and try again.",
        "PROTOCOL_MISMATCH"
      );
    }
    if (decoded.error) {
      throw new LocalDaemonError(decoded.error.message || "ComputeHop daemon rejected the request.", decoded.error.code);
    }
    return decoded;
  }
}

async function loadSchema() {
  if (!schemaPromise) {
    schemaPromise = protobuf.load(LOCAL_PROTO_PATH).then((root) => ({
      Request: root.lookupType("computehop.local.v1.Request"),
      Response: root.lookupType("computehop.local.v1.Response")
    }));
  }
  return schemaPromise;
}

async function loadCapabilityToken(tokenPath) {
  let info;
  try {
    info = await fs.lstat(tokenPath);
  } catch (error) {
    if (error.code === "ENOENT") {
      throw new LocalDaemonError("Start ComputeHop before using Control Center.", "DAEMON_NOT_RUNNING");
    }
    throw new LocalDaemonError(`Could not inspect ComputeHop credentials: ${error.message}`, "TOKEN_UNAVAILABLE");
  }
  if (!info.isFile() || info.isSymbolicLink()) {
    throw new LocalDaemonError("ComputeHop local credential is invalid.", "INVALID_TOKEN");
  }
  if (process.platform !== "win32" && (info.mode & 0o077) !== 0) {
    throw new LocalDaemonError("ComputeHop local credential must be owner-only.", "INVALID_TOKEN");
  }

  const encoded = (await fs.readFile(tokenPath, "utf8")).trim();
  const padded = encoded + "=".repeat((4 - (encoded.length % 4)) % 4);
  const token = Buffer.from(padded, "base64");
  if (token.length !== 32) {
    throw new LocalDaemonError("ComputeHop local credential is invalid.", "INVALID_TOKEN");
  }
  return token;
}

function sendFramedRequest(socketPath, payload, options = {}) {
  return new Promise((resolve, reject) => {
    if (!payload || payload.length === 0) {
      reject(new LocalDaemonError("Local IPC payload is empty.", "INVALID_FRAME"));
      return;
    }
    if (payload.length > MAX_FRAME_BYTES) {
      reject(new LocalDaemonError("Local IPC payload is too large.", "INVALID_FRAME"));
      return;
    }

    const socket = net.createConnection(socketPath);
    const timeoutMs = options.timeoutMs || DEFAULT_TIMEOUT_MS;
    let completed = false;
    let buffered = Buffer.alloc(0);
    let expectedPayloadBytes = null;

    const finish = (error, value) => {
      if (completed) {
        return;
      }
      completed = true;
      clearTimeout(timer);
      socket.destroy();
      if (options.signal) {
        options.signal.removeEventListener("abort", abort);
      }
      if (error) {
        reject(error);
      } else {
        resolve(value);
      }
    };

    const abort = () => {
      finish(new LocalDaemonError("Request stopped.", "ABORTED"));
    };

    const timer = setTimeout(() => {
      finish(new LocalDaemonError("ComputeHop daemon did not respond in time.", "TIMEOUT"));
    }, timeoutMs);

    if (options.signal) {
      if (options.signal.aborted) {
        abort();
        return;
      }
      options.signal.addEventListener("abort", abort, { once: true });
    }

    socket.once("connect", () => {
      const header = Buffer.alloc(4);
      header.writeUInt32BE(payload.length, 0);
      socket.write(Buffer.concat([header, Buffer.from(payload)]), (error) => {
        if (error) {
          finish(new LocalDaemonError(`Could not write to ComputeHop daemon: ${error.message}`, "TRANSPORT"));
        }
      });
    });

    socket.on("data", (chunk) => {
      buffered = Buffer.concat([buffered, chunk]);
      if (expectedPayloadBytes === null && buffered.length >= 4) {
        expectedPayloadBytes = buffered.readUInt32BE(0);
        if (expectedPayloadBytes === 0 || expectedPayloadBytes > MAX_FRAME_BYTES) {
          finish(new LocalDaemonError("ComputeHop daemon returned an invalid response.", "INVALID_FRAME"));
          return;
        }
      }

      if (expectedPayloadBytes !== null && buffered.length >= expectedPayloadBytes + 4) {
        finish(null, buffered.subarray(4, expectedPayloadBytes + 4));
      }
    });

    socket.once("error", (error) => {
      finish(new LocalDaemonError(`Could not reach ComputeHop daemon: ${error.message}`, "TRANSPORT"));
    });

    socket.once("close", () => {
      if (!completed) {
        finish(new LocalDaemonError("ComputeHop daemon closed the connection early.", "TRANSPORT"));
      }
    });
  });
}

function defaultStateDirectory() {
  if (process.env.COMPUTEHOP_STATE_DIR && path.isAbsolute(process.env.COMPUTEHOP_STATE_DIR)) {
    return process.env.COMPUTEHOP_STATE_DIR;
  }
  if (process.env.COMPUTEHOP_INTEGRATION_STATE_DIR && path.isAbsolute(process.env.COMPUTEHOP_INTEGRATION_STATE_DIR)) {
    return process.env.COMPUTEHOP_INTEGRATION_STATE_DIR;
  }
  switch (process.platform) {
    case "linux": {
      const stateHome = process.env.XDG_STATE_HOME;
      if (stateHome && path.isAbsolute(stateHome)) {
        return path.join(stateHome, "computehop");
      }
      return path.join(os.homedir(), ".local", "state", "computehop");
    }
    case "win32":
      return path.join(process.env.LOCALAPPDATA || path.join(os.homedir(), "AppData", "Local"), "ComputeHop");
    default:
      return path.join(os.homedir(), "Library", "Application Support", "ComputeHop");
  }
}

function jobStateLabel(job) {
  return String(job?.state || "JOB_STATE_UNSPECIFIED")
    .replace(/^JOB_STATE_/, "")
    .toLowerCase();
}

function jobSucceeded(job) {
  return job?.state === "JOB_STATE_SUCCEEDED";
}

function jobTerminal(job) {
  return [
    "JOB_STATE_SUCCEEDED",
    "JOB_STATE_FAILED",
    "JOB_STATE_CANCELLED",
    "JOB_STATE_REJECTED",
    "JOB_STATE_LOST"
  ].includes(job?.state);
}

module.exports = {
  LocalDaemonClient,
  LocalDaemonError,
  defaultStateDirectory,
  enumValues,
  jobStateLabel,
  jobSucceeded,
  jobTerminal,
  loadCapabilityToken
};
