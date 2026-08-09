const assert = require("node:assert/strict");
const test = require("node:test");
const { planFromSuggestion } = require("./suggestion-plan");

test("planFromSuggestion preserves metadata required for safe remote preflight", () => {
  assert.deepEqual(
    planFromSuggestion({
      task: "check CI",
      title: "Run project checks",
      command: "make pr-check",
      detail: "Use the repo's PR validation target.",
      requiresProject: true,
      outputs: ["dist/app.zip", ""],
      requiredToolIDs: ["go", "make"],
      targetPlatform: "darwin",
      targetArchitecture: "arm64",
      targetPreference: "worker",
      detected: ["Go", "Makefile"]
    }, "/Users/austin/project"),
    {
      source: "check CI",
      title: "Run project checks",
      command: "make pr-check",
      detail: "Use the repo's PR validation target.",
      requiresProject: true,
      outputs: ["dist/app.zip"],
      requiredToolIDs: ["go", "make"],
      targetPlatform: "darwin",
      targetArchitecture: "arm64",
      targetPreference: "worker",
      projectRoot: "/Users/austin/project",
      detected: ["Go", "Makefile"]
    }
  );
});

test("planFromSuggestion falls back to a compact title and empty optional fields", () => {
  assert.deepEqual(
    planFromSuggestion({
      label: "Build",
      command: "go build ./..."
    }, ""),
    {
      source: "Build",
      title: "Build",
      command: "go build ./...",
      detail: "",
      requiresProject: false,
      outputs: [],
      requiredToolIDs: [],
      targetPlatform: "",
      targetArchitecture: "",
      targetPreference: "",
      projectRoot: "",
      detected: []
    }
  );
});
