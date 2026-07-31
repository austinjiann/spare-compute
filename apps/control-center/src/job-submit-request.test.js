const assert = require("node:assert/strict");
const test = require("node:test");
const {
  normalizeExecutor,
  normalizeJobRequest,
  normalizeToolIDs
} = require("./job-submit-request");

test("normalizeJobRequest keeps native job requests minimal", () => {
  assert.deepEqual(
    normalizeJobRequest({
      command: " go test ./... ",
      deviceID: "worker-1",
      deviceName: "Gaming PC",
      workingDirectory: " /repo ",
      outputs: [" dist ", " report.xml "],
      requiredToolIDs: [" Go ", "go", "bad tool", "npm"]
    }),
    {
      command: "go test ./...",
      deviceID: "worker-1",
      deviceName: "Gaming PC",
      workingDirectory: "/repo",
      outputs: ["dist", "report.xml"],
      requiredToolIDs: ["go", "npm"],
      executor: "native",
      containerImage: ""
    }
  );
});

test("normalizeJobRequest preserves container executor details", () => {
  assert.deepEqual(
    normalizeJobRequest({
      command: "node --version",
      executor: "container",
      containerImage: " node:22-alpine ",
      requiredToolIds: ["docker"]
    }),
    {
      command: "node --version",
      deviceID: "local",
      deviceName: "",
      workingDirectory: "",
      outputs: [],
      requiredToolIDs: ["docker"],
      executor: "container",
      containerImage: "node:22-alpine"
    }
  );
});

test("normalizeJobRequest drops container image when the executor is native", () => {
  const request = normalizeJobRequest({
    command: "hostname",
    executor: "native",
    containerImage: "alpine:latest"
  });

  assert.equal(request.executor, "native");
  assert.equal(request.containerImage, "");
});

test("normalizeExecutor defaults unknown values to native", () => {
  assert.equal(normalizeExecutor("container"), "container");
  assert.equal(normalizeExecutor("CONTAINER"), "container");
  assert.equal(normalizeExecutor(""), "native");
  assert.equal(normalizeExecutor("docker"), "native");
});

test("normalizeToolIDs sorts, lowercases, dedupes, and removes unsafe values", () => {
  assert.deepEqual(normalizeToolIDs([" Go ", "node", "go", "BAD TOOL", "a=b", "npm"]), [
    "go",
    "node",
    "npm"
  ]);
});
