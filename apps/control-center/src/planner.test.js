const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { classifyIntent, planTask } = require("./planner");

test("planTask prefers Makefile PR check for CI", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n",
    Makefile: "pr-check:\n\tgo test ./...\n"
  });

  const result = await planTask({ task: "check ci", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.deepEqual(result.plan.detected.sort(), ["Go", "Makefile"].sort());
});

test("planTask uses detected package manager scripts", async (t) => {
  const project = await tempProject(t, {
    "package.json": JSON.stringify({ scripts: { build: "vite build", test: "vitest" } }),
    "pnpm-lock.yaml": ""
  });

  const build = await planTask({ task: "build the app", projectRoot: project });
  const tests = await planTask({ task: "run tests", projectRoot: project });

  assert.equal(build.ok, true);
  assert.equal(build.plan.command, "pnpm run build");
  assert.equal(tests.ok, true);
  assert.equal(tests.plan.command, "pnpm run test");
});

test("planTask maps Swift package tests", async (t) => {
  const project = await tempProject(t, {
    "Package.swift": "// swift-tools-version: 6.0\n"
  });

  const result = await planTask({ task: "run tests", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "swift test");
});

test("planTask preserves exact commands", async () => {
  const result = await planTask({ task: "go test ./...", projectRoot: "" });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "go test ./...");
  assert.equal(classifyIntent("go test ./..."), "exact");
});

test("planTask preserves exact make commands", async () => {
  const result = await planTask({ task: "make pr-check", projectRoot: "" });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.equal(classifyIntent("make pr-check"), "exact");
});

async function tempProject(t, files) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-planner-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  for (const [name, contents] of Object.entries(files)) {
    await fs.writeFile(path.join(root, name), contents);
  }
  return root;
}
