const assert = require("node:assert/strict");
const test = require("node:test");
const {
  jobDeviceName,
  jobOutputsForPlan,
  jobStartRequestForPlan,
  jobWorkingDirectoryForPlan,
  runReadinessError,
  runWorkingDirectory
} = require("./run-request");

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

test("jobOutputsForPlan combines planned and declared outputs", () => {
  assert.deepEqual(
    jobOutputsForPlan({
      plan: { outputs: [" dist/macos/ComputeHop.app ", "dist"] },
      outputs: ["dist", "report.pdf"]
    }),
    ["dist/macos/ComputeHop.app", "dist", "report.pdf"]
  );
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

test("jobStartRequestForPlan builds the daemon request for a remote project run", () => {
  assert.deepEqual(
    jobStartRequestForPlan({
      plan: {
        command: "go test ./...",
        requiresProject: true,
        outputs: ["coverage.out"]
      },
      device: {
        id: "auto",
        name: "Auto worker",
        workerName: "Austin MacBook 2"
      },
      projectRoot: "/Users/austin/project",
      outputs: [" report.xml ", "coverage.out", ""]
    }),
    {
      command: "go test ./...",
      deviceID: "auto",
      deviceName: "Austin MacBook 2",
      workingDirectory: "/Users/austin/project",
      outputs: ["coverage.out", "report.xml"]
    }
  );
});

test("jobDeviceName shows the backing worker for Auto worker runs", () => {
  assert.equal(
    jobDeviceName({
      id: "auto",
      name: "Auto worker",
      workerName: "Austin MacBook 2",
      detail: "Uses another worker"
    }),
    "Austin MacBook 2"
  );
  assert.equal(
    jobDeviceName({
      id: "auto",
      name: "Auto worker",
      detail: "Uses Gaming PC"
    }),
    "Gaming PC"
  );
  assert.equal(jobDeviceName({ id: "local", name: "This Mac" }), "This Mac");
});

test("jobStartRequestForPlan keeps remote utility runs projectless", () => {
  assert.deepEqual(
    jobStartRequestForPlan({
      plan: {
        command: "hostname",
        requiresProject: false
      },
      device: {
        id: "worker-1",
        name: "Gaming PC"
      },
      projectRoot: "/Users/austin/project",
      outputs: []
    }),
    {
      command: "hostname",
      deviceID: "worker-1",
      deviceName: "Gaming PC",
      workingDirectory: "",
      outputs: []
    }
  );
});

test("runReadinessError blocks missing or unusable run targets", () => {
  assert.equal(
    runReadinessError({
      device: null,
      canRun: false
    }),
    "Choose This Mac or a connected worker first."
  );
  assert.equal(
    runReadinessError({
      device: { id: "worker-1", name: "Offline Mac" },
      canRun: false
    }),
    "Choose This Mac or a connected worker first."
  );
});

test("runReadinessError explains pending selected workers", () => {
  assert.equal(
    runReadinessError({
      device: {
        id: "worker-1",
        name: "Gaming PC",
        unavailableSelection: true
      },
      canRun: false
    }),
    "Gaming PC is not available yet. Keep the worker app open, or switch to This Mac."
  );
});

test("runReadinessError requires a project before project work on any device", () => {
  assert.equal(
    runReadinessError({
      device: { id: "local", name: "This Mac" },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: true },
      projectRoot: ""
    }),
    "Choose a project before running this. ComputeHop needs the folder so it can run from the right place."
  );
  assert.match(
    runReadinessError({
      device: { id: "worker-1", name: "Gaming PC" },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: true },
      projectRoot: ""
    }),
    /Choose a project before running this on Gaming PC/
  );
});

test("runReadinessError requires a project before output restoration on any device", () => {
  assert.equal(
    runReadinessError({
      device: { id: "local", name: "This Mac" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false },
      outputs: ["dist"],
      projectRoot: ""
    }),
    "Choose a project before bringing files back. ComputeHop needs the folder those outputs belong to."
  );
  assert.equal(
    runReadinessError({
      device: { id: "worker-1", name: "Gaming PC" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false },
      outputs: ["dist"],
      projectRoot: ""
    }),
    "Choose a project before bringing files back from another computer."
  );
});

test("runReadinessError allows remote utility runs without a project", () => {
  assert.equal(
    runReadinessError({
      device: { id: "worker-1", name: "Gaming PC" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false },
      outputs: [],
      projectRoot: ""
    }),
    ""
  );
});

test("runReadinessError returns selected device policy failures last", () => {
  assert.equal(
    runReadinessError({
      device: { id: "local", name: "This Mac" },
      canRun: true,
      plan: { command: "docker build .", requiresProject: false },
      policyError: "Docker is turned off for this computer."
    }),
    "Docker is turned off for this computer."
  );
});
