const assert = require("node:assert/strict");
const test = require("node:test");
const {
  catalogSchemaVersion,
  commonToolIDs,
  normalizeToolIDs,
  toolDescriptors,
  toolLabel,
  toolListLabel
} = require("./capability-catalog");

test("capability catalog exposes a stable schema and sorted common tool IDs", () => {
  assert.equal(catalogSchemaVersion, 1);
  assert.equal(toolDescriptors.every((descriptor) => descriptor.schemaVersion === 1), true);
  assert.deepEqual(commonToolIDs(), [...commonToolIDs()].sort());
  assert.equal(commonToolIDs().includes("pytest"), true);
  assert.equal(commonToolIDs().includes("uv"), true);
});

test("capability catalog normalizes and labels tool IDs", () => {
  assert.deepEqual(normalizeToolIDs([" Go ", "docker", "go", "", "bad tool"]), ["docker", "go"]);
  assert.equal(toolLabel("go"), "Go");
  assert.equal(toolLabel("xcodebuild"), "Xcode");
  assert.equal(toolLabel("custom-tool"), "custom-tool");
  assert.equal(toolListLabel(["go"]), "Go");
  assert.equal(toolListLabel(["go", "docker"]), "Docker and Go");
  assert.equal(toolListLabel(["go", "docker", "swift"]), "Docker, Go, and Swift");
});
