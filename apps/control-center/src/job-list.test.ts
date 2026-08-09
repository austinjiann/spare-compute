const assert = require("node:assert/strict");
const test = require("node:test");
const { mergeJobRefresh } = require("./job-list");

test("mergeJobRefresh keeps fetched jobs first and replaces matching existing jobs", () => {
  assert.deepEqual(
    mergeJobRefresh(
      [
        job("job-1", { state: "running", progress: "upload 75%" }),
        job("job-2", { state: "succeeded", terminal: true })
      ],
      [
        job("job-1", { state: "running", progress: "upload 50%" }),
        job("job-3", { state: "running" })
      ]
    ).map((value) => [value.id, value.progress || value.state]),
    [
      ["job-1", "upload 75%"],
      ["job-2", "succeeded"],
      ["job-3", "running"]
    ]
  );
});

test("mergeJobRefresh preserves missing non-terminal jobs but drops stale terminal jobs", () => {
  assert.deepEqual(
    mergeJobRefresh(
      [job("job-1", { state: "running" })],
      [
        job("job-2", { state: "queued" }),
        job("job-3", { state: "succeeded", terminal: true })
      ]
    ).map((value) => value.id),
    ["job-1", "job-2"]
  );
});

test("mergeJobRefresh preserves UI-only metadata when daemon rows replace active jobs", () => {
  const merged = mergeJobRefresh(
    [
      job("job-1", {
        state: "running",
        progress: "upload 75%",
        deviceID: "",
        deviceName: "",
        workingDirectory: ""
      })
    ],
    [
      job("job-1", {
        state: "running",
        progress: "upload 50%",
        deviceID: "worker-1",
        deviceName: "Gaming PC",
        workingDirectory: "/Users/austin/project"
      })
    ]
  );

  assert.equal(merged[0].progress, "upload 75%");
  assert.equal(merged[0].deviceID, "worker-1");
  assert.equal(merged[0].deviceName, "Gaming PC");
  assert.equal(merged[0].workingDirectory, "/Users/austin/project");
});

test("mergeJobRefresh ignores duplicate and malformed rows", () => {
  assert.deepEqual(
    mergeJobRefresh(
      [job("job-1"), job("job-1", { progress: "ignored" }), {}],
      [job("job-1"), job("job-2")]
    ).map((value) => value.id),
    ["job-1", "job-2"]
  );
});

function job(id, overrides: any = {}) {
  return {
    id,
    state: "running",
    terminal: false,
    ...overrides
  };
}
