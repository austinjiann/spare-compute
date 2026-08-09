const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  commandNeedsProject,
  detectedLabels,
  inspectProject,
  requiredToolIDsForCommand,
  stripPlacementSuffix,
  targetArchitectureForTask,
  targetPlatformForTask,
  targetPreferenceForTask
} = require("./planner");

test("inspectProject exposes grounded project metadata without choosing a command", async (t) => {
  const project = await tempProject(t, {
    "package.json": JSON.stringify({ scripts: { build: "vite build", test: "vitest" } }),
    "pnpm-lock.yaml": "",
    "go.mod": "module example.com/app\n",
    Makefile: [
      "pr-check: test deploy-check",
      "test:",
      "\tgo test ./...",
      "deploy-check:",
      "\tdocker compose config --quiet"
    ].join("\n")
  });

  const profile = await inspectProject(project);

  assert.equal(profile.root, project);
  assert.equal(profile.packageManager, "pnpm");
  assert.deepEqual(Object.keys(profile.packageScripts).sort(), ["build", "test"]);
  assert.deepEqual(profile.makeTargets, ["pr-check", "test", "deploy-check"]);
  assert.deepEqual(detectedLabels(profile).sort(), ["Go", "Makefile", "pnpm package"].sort());
  assert.deepEqual(requiredToolIDsForCommand("make pr-check", profile), [
    "docker",
    "go",
    "make",
    "node",
    "pnpm"
  ]);
});

test("requiredToolIDsForCommand keeps executable preflight hints", () => {
  assert.deepEqual(requiredToolIDsForCommand("pnpm run test"), ["node", "pnpm"]);
  assert.deepEqual(requiredToolIDsForCommand("docker compose build"), ["docker"]);
  assert.deepEqual(requiredToolIDsForCommand("go test ./..."), ["go"]);
  assert.deepEqual(requiredToolIDsForCommand("./scripts/check"), []);
});

test("commandNeedsProject recognizes project-scoped commands", () => {
  assert.equal(commandNeedsProject("make check"), true);
  assert.equal(commandNeedsProject("go test ./..."), true);
  assert.equal(commandNeedsProject("docker compose build"), true);
  assert.equal(commandNeedsProject("hostname"), false);
});

test("placement helpers preserve routing constraints and literal arguments", () => {
  assert.equal(targetPreferenceForTask("run tests on the worker"), "worker");
  assert.equal(targetPreferenceForTask("run tests here"), "local");
  assert.equal(targetPreferenceForTask("echo here"), "");
  assert.equal(targetPlatformForTask("build on Windows"), "windows");
  assert.equal(targetPlatformForTask("build on Apple Silicon"), "darwin");
  assert.equal(targetArchitectureForTask("build on Apple Silicon"), "arm64");
  assert.equal(targetArchitectureForTask("build on x64"), "amd64");
  assert.equal(stripPlacementSuffix("make check on the worker"), "make check");
  assert.equal(stripPlacementSuffix("hostname on Windows"), "hostname");
  assert.equal(stripPlacementSuffix("echo on Windows"), "echo on Windows");
});

async function tempProject(t, files) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-planner-context-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  for (const [name, contents] of Object.entries(files)) {
    await fs.writeFile(path.join(root, name), contents);
  }
  return root;
}
