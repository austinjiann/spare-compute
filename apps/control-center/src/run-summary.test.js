const assert = require("node:assert/strict");
const test = require("node:test");
const {
  initialRunMessage,
  runSummaryLines,
  shortPath
} = require("./run-summary");

test("runSummaryLines explains a remote project run without route jargon", () => {
  assert.deepEqual(
    runSummaryLines({
      plan: { command: "make pr-check", requiresProject: true },
      device: { id: "auto", name: "Auto worker", workerName: "Gaming PC" },
      projectRoot: "/Users/austin/spare-compute",
      outputs: []
    }),
    ["Runs on Gaming PC", "Copies austin/spare-compute"]
  );
});

test("runSummaryLines keeps remote utility jobs projectless", () => {
  assert.deepEqual(
    runSummaryLines({
      plan: { command: "hostname", requiresProject: false },
      device: { id: "worker-1", name: "Mini PC" },
      projectRoot: "/Users/austin/spare-compute",
      outputs: []
    }),
    ["Runs on Mini PC", "No project files"]
  );
});

test("runSummaryLines includes declared outputs once", () => {
  assert.deepEqual(
    runSummaryLines({
      plan: { command: "make package", requiresProject: true },
      device: { id: "local", name: "This Mac" },
      projectRoot: "/Users/austin/spare-compute",
      outputs: ["dist", "report.pdf", "dist"]
    }),
    ["Runs here", "Uses austin/spare-compute", "Brings back dist, report.pdf"]
  );
});

test("initialRunMessage summarizes the submitted job", () => {
  assert.equal(
    initialRunMessage({
      command: "make pr-check",
      deviceName: "Gaming PC",
      workingDirectory: "/Users/austin/spare-compute",
      outputs: ["dist", "coverage.out"]
    }),
    "Running make pr-check on Gaming PC…\nProject: austin/spare-compute\nWill bring back: dist, coverage.out"
  );
});

test("shortPath keeps small paths and compacts long paths", () => {
  assert.equal(shortPath("spare-compute"), "spare-compute");
  assert.equal(shortPath("/Users/austin/spare-compute"), "austin/spare-compute");
  assert.equal(shortPath("C:\\Users\\Austin\\project"), "Austin/project");
});
