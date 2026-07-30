const assert = require("node:assert/strict");
const test = require("node:test");
const { runWorkingDirectory } = require("./run-request");

test("runWorkingDirectory preserves an explicit project folder", () => {
  assert.equal(runWorkingDirectory({ workingDirectory: "/Users/austin/project" }), "/Users/austin/project");
});

test("runWorkingDirectory does not invent a development fallback without a project", () => {
  assert.equal(runWorkingDirectory({ workingDirectory: "" }), "");
  assert.equal(runWorkingDirectory({}), "");
  assert.equal(runWorkingDirectory(null), "");
});
