const assert = require("node:assert/strict");
const test = require("node:test");
const { mapJob } = require("./job-summary");

test("mapJob preserves the submitted working directory for output restore defaults", () => {
  const mapped = mapJob(
    {
      id: "job-1",
      spec: {
        executable: "make",
        arguments: ["macos-package"],
        workingDirectory: "/Users/austin/project-a",
        outputs: ["dist/macos/ComputeHop.app"]
      },
      state: "JOB_STATE_SUCCEEDED",
      createdAtUnixNano: 1_700_000_000_000_000_000,
      updatedAtUnixNano: 1_700_000_001_000_000_000
    },
    "worker-1"
  );

  assert.equal(mapped.workingDirectory, "/Users/austin/project-a");
  assert.equal(mapped.command, "make macos-package");
  assert.equal(mapped.canFetchOutputs, true);
  assert.deepEqual(mapped.outputs, ["dist/macos/ComputeHop.app"]);
  assert.equal(mapped.deviceID, "worker-1");
});

test("mapJob keeps projectless utility jobs projectless", () => {
  const mapped = mapJob({
    id: "job-2",
    spec: {
      executable: "hostname",
      arguments: [],
      workingDirectory: "",
      outputs: []
    },
    state: "JOB_STATE_SUCCEEDED"
  });

  assert.equal(mapped.workingDirectory, "");
  assert.equal(mapped.canFetchOutputs, false);
  assert.equal(mapped.deviceID, "local");
});
