const assert = require("node:assert/strict");
const test = require("node:test");
const {
  jobDeviceName,
  jobOutputsForPlan,
  jobStartRequestForPlan,
  jobWorkingDirectoryForPlan,
  outputValidationForPlan,
  runReadinessBlocker,
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

test("outputValidationForPlan rejects unsafe output declarations", () => {
  assert.equal(outputValidationForPlan({ outputs: ["dist", "report.pdf"] }).ok, true);
  assert.match(outputValidationForPlan({ outputs: ["/tmp/report.pdf"] }).error, /relative/);
  assert.match(outputValidationForPlan({ outputs: ["dist", "Dist"] }).error, /collide/);
  assert.equal(
    outputValidationForPlan({
      plan: { ignoreDeclaredOutputs: true },
      outputs: ["/tmp/report.pdf"]
    }).ok,
    true
  );
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
      daemonAvailable: false,
      device: { id: "local", name: "This Mac" },
      canRun: true
    }),
    "Start ComputeHop before running jobs."
  );
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

test("runReadinessBlocker suggests starting ComputeHop when the daemon is unavailable", () => {
  assert.deepEqual(
    runReadinessBlocker({
      daemonAvailable: false,
      device: { id: "local", name: "This Mac" },
      canRun: true
    }),
    {
      message: "Start ComputeHop before running jobs.",
      actionKind: "start-daemon",
      actionLabel: "Start"
    }
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
    "Gaming PC is not reachable. Open ComputeHop on that computer and keep both computers on the same network, then try again. For different networks, set up VPS connectivity."
  );
});

test("runReadinessError explains selected offline workers before submission", () => {
  assert.equal(
    runReadinessError({
      device: {
        id: "worker-1",
        name: "Gaming PC",
        role: "worker",
        trustState: "paired",
        connection: "not connected",
        availability: "offline"
      },
      canRun: false
    }),
    "Gaming PC is not reachable. Open ComputeHop on that computer and keep both computers on the same network, then try again. For different networks, set up VPS connectivity."
  );
});

test("runReadinessBlocker explains selected paused workers before submission", () => {
  assert.deepEqual(
    runReadinessBlocker({
      device: {
        id: "worker-1",
        name: "Gaming PC",
        role: "worker",
        trustState: "paired",
        connection: "active",
        availability: "remote",
        synced: false
      },
      canRun: false
    }),
    {
      message: "Gaming PC is paused for tasks. Enable it in Devices, or switch to This Mac.",
      actionKind: "enable-device",
      actionLabel: "Enable"
    }
  );
});

test("runReadinessBlocker explains selected nearby workers before submission", () => {
  assert.deepEqual(
    runReadinessBlocker({
      device: {
        id: "presence-1",
        name: "Home Server",
        role: "worker",
        trustState: "unpaired",
        connection: "not connected",
        availability: "nearby"
      },
      canRun: false
    }),
    {
      message: "Home Server is nearby but not connected. Connect it from Devices first, or switch to This Mac.",
      actionKind: "connect-device",
      actionLabel: "Connect"
    }
  );
});

test("runReadinessBlocker explains selected reconnecting workers before submission", () => {
  assert.deepEqual(
    runReadinessBlocker({
      device: {
        id: "worker-1",
        name: "Mini PC",
        role: "worker",
        trustState: "paired",
        connection: "active",
        availability: "connecting"
      },
      canRun: false
    }),
    {
      message: "Mini PC is still connecting. Wait a moment, then try again.",
      actionKind: "refresh",
      actionLabel: "Refresh"
    }
  );
});

test("runReadinessBlocker prevents worker-targeted plans from running locally", () => {
  assert.deepEqual(
    runReadinessBlocker({
      device: { id: "local", name: "This Mac" },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: true, targetPreference: "worker" },
      projectRoot: "/project"
    }),
    {
      message: "This task was asked to run on another computer. Connect a worker or choose a worker from Devices first.",
      actionKind: "refresh",
      actionLabel: "Refresh"
    }
  );
});

test("runReadinessBlocker prevents local-targeted plans from running remotely", () => {
  assert.equal(
    runReadinessError({
      device: { id: "worker-1", name: "Gaming PC", role: "worker", connection: "active", availability: "remote", trustState: "paired" },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: true, targetPreference: "local" },
      projectRoot: "/project"
    }),
    "This task was asked to run here. Switch the run target to This Mac first."
  );
});

test("runReadinessBlocker prevents OS-targeted plans from running on the wrong platform", () => {
  assert.equal(
    runReadinessError({
      device: { id: "local", name: "This Mac", platform: "darwin" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false, targetPlatform: "windows" },
      projectRoot: ""
    }),
    "This task needs Windows. Choose a Windows computer first."
  );
  assert.equal(
    runReadinessError({
      device: {
        id: "worker-1",
        name: "Gaming PC",
        role: "worker",
        platform: "win32",
        connection: "active",
        availability: "remote",
        trustState: "paired"
      },
      canRun: true,
      plan: { command: "hostname", requiresProject: false, targetPlatform: "windows" },
      projectRoot: ""
    }),
    ""
  );
  assert.equal(
    runReadinessError({
      device: {
        id: "worker-1",
        name: "Unknown worker",
        role: "worker",
        connection: "active",
        availability: "remote",
        trustState: "paired"
      },
      canRun: true,
      plan: { command: "hostname", requiresProject: false, targetPlatform: "linux" },
      projectRoot: ""
    }),
    ""
  );
});

test("runReadinessBlocker prevents architecture-targeted plans from running on the wrong architecture", () => {
  assert.equal(
    runReadinessError({
      device: { id: "local", name: "This Mac", platform: "darwin", arch: "arm64" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false, targetArchitecture: "amd64" },
      projectRoot: ""
    }),
    "This task needs x64. Choose an x64 computer first."
  );
  assert.equal(
    runReadinessError({
      device: {
        id: "worker-1",
        name: "M-series Mac",
        role: "worker",
        platform: "darwin",
        arch: "aarch64",
        connection: "active",
        availability: "remote",
        trustState: "paired"
      },
      canRun: true,
      plan: { command: "hostname", requiresProject: false, targetArchitecture: "arm64" },
      projectRoot: ""
    }),
    ""
  );
  assert.equal(
    runReadinessError({
      device: {
        id: "worker-1",
        name: "Unknown worker",
        role: "worker",
        connection: "active",
        availability: "remote",
        trustState: "paired"
      },
      canRun: true,
      plan: { command: "hostname", requiresProject: false, targetArchitecture: "arm64" },
      projectRoot: ""
    }),
    ""
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

test("runReadinessBlocker suggests choosing a project when project input is required", () => {
  assert.deepEqual(
    runReadinessBlocker({
      device: { id: "worker-1", name: "Gaming PC" },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: true },
      projectRoot: ""
    }),
    {
      message: "Choose a project before running this on Gaming PC. ComputeHop needs the folder so it can copy the files to that computer.",
      actionKind: "choose-project",
      actionLabel: "Choose project"
    }
  );
  assert.deepEqual(
    runReadinessBlocker({
      device: { id: "worker-1", name: "Gaming PC" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false },
      outputs: ["dist"],
      projectRoot: ""
    }),
    {
      message: "Choose a project before bringing files back from another computer.",
      actionKind: "choose-project",
      actionLabel: "Choose project"
    }
  );
});

test("runReadinessError requires a project before output restoration on any device", () => {
  assert.match(
    runReadinessError({
      device: { id: "local", name: "This Mac" },
      canRun: true,
      plan: { command: "hostname", requiresProject: false },
      projectRoot: "/Users/austin/project",
      outputs: ["/tmp/report.pdf"]
    }),
    /relative/
  );
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

test("runReadinessError explains reported missing tools", () => {
  assert.equal(
    runReadinessError({
      device: { id: "worker-1", name: "Gaming PC", toolIDs: ["node"] },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: false },
      outputs: [],
      projectRoot: ""
    }),
    "Gaming PC does not report go. Choose another computer or install it there."
  );
  assert.equal(
    runReadinessError({
      device: { id: "worker-1", name: "Old worker", toolIDs: [] },
      canRun: true,
      plan: { command: "go test ./...", requiresProject: false },
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
