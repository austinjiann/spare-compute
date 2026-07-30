const assert = require("node:assert/strict");
const test = require("node:test");
const { remotePreparationMessage } = require("./run-feedback");

test("remotePreparationMessage describes remote project snapshot work", () => {
  assert.equal(
    remotePreparationMessage({
      deviceSelector: "5wc2jkni",
      deviceName: "Austin MacBook 2",
      workingDirectory: "/Users/austin/project"
    }),
    "Preparing remote run for Austin MacBook 2 from /Users/austin/project; snapshot/upload may take a moment."
  );
});

test("remotePreparationMessage falls back to selector when the display name is missing", () => {
  assert.equal(
    remotePreparationMessage({
      deviceSelector: "5wc2jkni",
      workingDirectory: "/project"
    }),
    "Preparing remote run for 5wc2jkni from /project; snapshot/upload may take a moment."
  );
});

test("remotePreparationMessage stays silent for local or projectless runs", () => {
  assert.equal(remotePreparationMessage({ deviceSelector: "", workingDirectory: "/project" }), "");
  assert.equal(remotePreparationMessage({ deviceSelector: "worker-1", workingDirectory: "" }), "");
});
