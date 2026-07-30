const assert = require("node:assert/strict");
const test = require("node:test");
const { jobOutputsForPlan, jobWorkingDirectoryForPlan, runWorkingDirectory } = require("./run-request");

test("runWorkingDirectory preserves an explicit project folder", () => {
  assert.equal(runWorkingDirectory({ workingDirectory: "/Users/austin/project" }), "/Users/austin/project");
});

test("runWorkingDirectory does not invent a development fallback without a project", () => {
  assert.equal(runWorkingDirectory({ workingDirectory: "" }), "");
  assert.equal(runWorkingDirectory({}), "");
  assert.equal(runWorkingDirectory(null), "");
});

test("jobWorkingDirectoryForPlan attaches a project only when the plan needs project files", () => {
  assert.equal(
    jobWorkingDirectoryForPlan({
      projectRoot: "/Users/austin/project",
      plan: { requiresProject: true },
      outputs: []
    }),
    "/Users/austin/project"
  );
  assert.equal(
    jobWorkingDirectoryForPlan({
      projectRoot: "/Users/austin/project",
      plan: { requiresProject: false },
      outputs: []
    }),
    ""
  );
});

test("jobWorkingDirectoryForPlan keeps a project for declared outputs", () => {
  assert.equal(
    jobWorkingDirectoryForPlan({
      projectRoot: "/Users/austin/project",
      plan: { requiresProject: false },
      outputs: ["dist"]
    }),
    "/Users/austin/project"
  );
});

test("jobWorkingDirectoryForPlan ignores outputs for connection tests", () => {
  assert.equal(
    jobWorkingDirectoryForPlan({
      projectRoot: "/Users/austin/project",
      plan: { requiresProject: false, ignoreDeclaredOutputs: true },
      outputs: ["dist"]
    }),
    ""
  );
});

test("jobWorkingDirectoryForPlan stays empty without a selected project", () => {
  assert.equal(
    jobWorkingDirectoryForPlan({
      projectRoot: "",
      plan: { requiresProject: true },
      outputs: ["dist"]
    }),
    ""
  );
});

test("jobOutputsForPlan normalizes declared outputs", () => {
  assert.deepEqual(jobOutputsForPlan({ outputs: [" dist ", "", "report", "dist"] }), [
    "dist",
    "report"
  ]);
});

test("jobOutputsForPlan drops outputs when the plan opts out", () => {
  assert.deepEqual(
    jobOutputsForPlan({
      plan: { ignoreDeclaredOutputs: true },
      outputs: ["dist"]
    }),
    []
  );
});
