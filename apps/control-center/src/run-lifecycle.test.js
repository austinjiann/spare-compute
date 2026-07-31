const assert = require("node:assert/strict");
const test = require("node:test");
const {
  detachActiveRun,
  detachAllRuns,
  detachRunsForWebContents,
  runIDsForWebContents,
  stopActiveRun,
  stopAllRuns
} = require("./run-lifecycle");

test("stopActiveRun aborts and cancels a submitted job", async () => {
  let aborted = false;
  const cancellations = [];
  const activeRuns = new Map([
    ["run-1", {
      abortController: { abort: () => { aborted = true; } },
      client: {
        cancelJob: async (jobID, options) => {
          cancellations.push({ jobID, options });
        }
      },
      deviceSelector: "worker-1",
      jobID: "job-1",
      stopped: false
    }]
  ]);

  const result = await stopActiveRun(activeRuns, "run-1");

  assert.deepEqual(result, { stopped: true, cancelled: true });
  assert.equal(aborted, true);
  assert.equal(activeRuns.get("run-1").stopped, true);
  assert.deepEqual(cancellations, [
    { jobID: "job-1", options: { deviceSelector: "worker-1" } }
  ]);
});

test("stopActiveRun stops pre-submit runs without cancelling a missing job", async () => {
  let cancelled = false;
  const activeRuns = new Map([
    ["run-1", {
      abortController: new AbortController(),
      client: { cancelJob: async () => { cancelled = true; } },
      deviceSelector: "",
      jobID: "",
      stopped: false
    }]
  ]);

  const result = await stopActiveRun(activeRuns, "run-1");

  assert.deepEqual(result, { stopped: true, cancelled: false });
  assert.equal(cancelled, false);
  assert.equal(activeRuns.get("run-1").stopped, true);
  assert.equal(activeRuns.get("run-1").abortController.signal.aborted, true);
});

test("stopActiveRun reports cancellation failures without throwing", async () => {
  const failure = new Error("worker disappeared");
  const reports = [];
  const activeRuns = new Map([
    ["run-1", {
      abortController: new AbortController(),
      client: { cancelJob: async () => { throw failure; } },
      deviceSelector: "worker-1",
      jobID: "job-1",
      stopped: false,
      webContents: { id: 7 }
    }]
  ]);

  const result = await stopActiveRun(activeRuns, "run-1", {
    onCancelError: (record, runID, error) => {
      reports.push({ webContentsID: record.webContents.id, runID, error });
    }
  });

  assert.equal(result.stopped, true);
  assert.equal(result.cancelled, false);
  assert.equal(result.error, failure);
  assert.deepEqual(reports, [{ webContentsID: 7, runID: "run-1", error: failure }]);
});

test("detachActiveRun aborts stream tracking without cancelling submitted daemon jobs", () => {
  let cancelled = false;
  const activeRuns = new Map([
    ["run-1", {
      abortController: new AbortController(),
      client: { cancelJob: async () => { cancelled = true; } },
      deviceSelector: "worker-1",
      jobID: "job-1",
      stopped: false
    }]
  ]);

  const result = detachActiveRun(activeRuns, "run-1");

  assert.deepEqual(result, { detached: true, submitted: true });
  assert.equal(cancelled, false);
  assert.equal(activeRuns.has("run-1"), false);
});

test("detachRunsForWebContents detaches only runs owned by the closed window", () => {
  const firstWindow = { id: 1 };
  const secondWindow = { id: 2 };
  const stopped = [];
  const activeRuns = new Map([
    ["run-1", record(firstWindow, stopped, "job-run-1")],
    ["run-2", record({ id: 1 }, stopped, "job-run-2")],
    ["run-3", record(secondWindow, stopped, "job-run-3")]
  ]);

  assert.deepEqual(runIDsForWebContents(activeRuns, firstWindow), ["run-1", "run-2"]);

  const result = detachRunsForWebContents(activeRuns, firstWindow);

  assert.equal(result.detached, 2);
  assert.equal(result.submitted, 2);
  assert.deepEqual(stopped, []);
  assert.equal(activeRuns.has("run-1"), false);
  assert.equal(activeRuns.has("run-2"), false);
  assert.equal(activeRuns.get("run-3").stopped, false);
});

test("detachAllRuns detaches every active stream without cancellation", () => {
  const stopped = [];
  const activeRuns = new Map([
    ["run-1", record({ id: 1 }, stopped, "job-run-1")],
    ["run-2", record({ id: 2 }, stopped, "job-run-2")]
  ]);

  const result = detachAllRuns(activeRuns);

  assert.equal(result.detached, 2);
  assert.equal(result.submitted, 2);
  assert.deepEqual(stopped, []);
  assert.equal(activeRuns.size, 0);
});

test("stopAllRuns stops every active run", async () => {
  const stopped = [];
  const activeRuns = new Map([
    ["run-1", record({ id: 1 }, stopped, "job-run-1")],
    ["run-2", record({ id: 2 }, stopped, "job-run-2")]
  ]);

  const result = await stopAllRuns(activeRuns);

  assert.equal(result.stopped, 2);
  assert.equal(result.cancelled, 2);
  assert.deepEqual(stopped, ["job-run-1", "job-run-2"]);
});

function record(webContents, stopped, jobID) {
  return {
    abortController: new AbortController(),
    client: {
      cancelJob: async (jobID) => {
        stopped.push(jobID);
      }
    },
    deviceSelector: "worker",
    jobID,
    stopped: false,
    webContents
  };
}
