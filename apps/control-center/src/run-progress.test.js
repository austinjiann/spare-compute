const assert = require("node:assert/strict");
const test = require("node:test");
const {
  jobUpdateSignature,
  nextJobUpdate
} = require("./run-progress");

test("jobUpdateSignature tracks user-visible job changes", () => {
  const first = jobUpdateSignature({
    id: "job-1",
    state: "running",
    progress: "upload 50%",
    updated: "2026-07-30T15:00:00Z"
  });
  const second = jobUpdateSignature({
    id: "job-1",
    state: "running",
    progress: "upload 75%",
    updated: "2026-07-30T15:00:01Z"
  });

  assert.notEqual(first, "");
  assert.notEqual(first, second);
  assert.equal(jobUpdateSignature({ state: "running" }), "");
});

test("nextJobUpdate suppresses duplicate progress events", () => {
  const job = {
    id: "job-1",
    state: "running",
    progress: "download 50%",
    updated: "2026-07-30T15:00:00Z"
  };
  const first = nextJobUpdate("", job);
  const duplicate = nextJobUpdate(first.signature, job);
  const changed = nextJobUpdate(first.signature, { ...job, progress: "download 100%" });

  assert.equal(first.changed, true);
  assert.equal(duplicate.changed, false);
  assert.equal(changed.changed, true);
});
