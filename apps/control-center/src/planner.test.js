const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { classifyIntent, commandNeedsProject, planTask, suggestTasks } = require("./planner");

test("planTask prefers Makefile PR check for CI", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n",
    Makefile: "pr-check:\n\tgo test ./...\n"
  });

  const result = await planTask({ task: "fix ci", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.equal(result.plan.requiresProject, true);
  assert.deepEqual(result.plan.detected.sort(), ["Go", "Makefile"].sort());
  assert.equal(classifyIntent("fix ci"), "ci");
  assert.equal(classifyIntent("validate project"), "ci");
  assert.equal(classifyIntent("preflight"), "ci");
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
  assert.equal(build.plan.requiresProject, true);
  assert.equal(tests.ok, true);
  assert.equal(tests.plan.command, "pnpm run test");
  assert.equal(tests.plan.requiresProject, true);
});

test("planTask prefers app packaging targets for package requests", async (t) => {
  const project = await tempProject(t, {
    Makefile: "macos-archive:\n\tpackaging/macos/archive.sh\nmacos-package:\n\tpackaging/macos/build.sh\n",
    "go.mod": "module example.com/app\n"
  });

  const result = await planTask({ task: "package the app", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.title, "Package macOS app");
  assert.equal(result.plan.command, "make macos-archive");
  assert.equal(result.plan.requiresProject, true);
  assert.deepEqual(result.plan.outputs, [
    "dist/macos/ComputeHop-macos.zip",
    "dist/macos/ComputeHop-macos.zip.sha256"
  ]);
  assert.equal(classifyIntent("package the app"), "package");
});

test("suggestTasks includes package targets with inferred outputs", async (t) => {
  const project = await tempProject(t, {
    Makefile: "macos-archive:\n\tpackaging/macos/archive.sh\nmacos-package:\n\tpackaging/macos/build.sh\n"
  });

  const result = await suggestTasks({ projectRoot: project });
  const suggestion = result.suggestions.find((value) => value.id === "package");

  assert.equal(result.ok, true);
  assert.equal(suggestion.label, "Package");
  assert.equal(suggestion.command, "make macos-archive");
  assert.deepEqual(suggestion.outputs, [
    "dist/macos/ComputeHop-macos.zip",
    "dist/macos/ComputeHop-macos.zip.sha256"
  ]);
});

test("planTask maps Swift package tests", async (t) => {
  const project = await tempProject(t, {
    "Package.swift": "// swift-tools-version: 6.0\n"
  });

  const result = await planTask({ task: "run tests", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "swift test");
  assert.equal(result.plan.requiresProject, true);
});

test("planTask falls back to language lint commands", async (t) => {
  const goProject = await tempProject(t, {
    "go.mod": "module example.com/app\n"
  });
  const rustProject = await tempProject(t, {
    "Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n"
  });
  const pythonProject = await tempProject(t, {
    "pyproject.toml": "[project]\nname = \"app\"\n"
  });

  const go = await planTask({ task: "lint project", projectRoot: goProject });
  const rust = await planTask({ task: "check style", projectRoot: rustProject });
  const python = await planTask({ task: "format check", projectRoot: pythonProject });

  assert.equal(go.ok, true);
  assert.equal(go.plan.command, "go vet ./...");
  assert.equal(go.plan.requiresProject, true);
  assert.equal(rust.ok, true);
  assert.equal(rust.plan.command, "cargo clippy");
  assert.equal(rust.plan.requiresProject, true);
  assert.equal(python.ok, true);
  assert.equal(python.plan.command, "ruff check .");
  assert.equal(python.plan.requiresProject, true);
});

test("planTask maps Docker build requests", async (t) => {
  const project = await tempProject(t, {
    Dockerfile: "FROM alpine\n"
  });

  const result = await planTask({ task: "build docker image", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "docker build .");
  assert.equal(result.plan.requiresProject, true);
  assert.deepEqual(result.plan.detected, ["Docker"]);
});

test("planTask maps Compose build requests", async (t) => {
  const project = await tempProject(t, {
    "compose.yaml": "services:\n  app:\n    build: .\n"
  });

  const result = await planTask({ task: "build containers", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "docker compose build");
  assert.equal(result.plan.requiresProject, true);
  assert.deepEqual(result.plan.detected, ["Compose"]);
});

test("planTask lets explicit Docker build requests override package scripts", async (t) => {
  const project = await tempProject(t, {
    "package.json": JSON.stringify({ scripts: { build: "vite build" } }),
    Dockerfile: "FROM alpine\n"
  });

  const appBuild = await planTask({ task: "build the app", projectRoot: project });
  const dockerBuild = await planTask({ task: "build docker image", projectRoot: project });

  assert.equal(appBuild.ok, true);
  assert.equal(appBuild.plan.command, "npm run build");
  assert.equal(dockerBuild.ok, true);
  assert.equal(dockerBuild.plan.command, "docker build .");
});

test("suggestTasks returns project-aware task chips", async (t) => {
  const project = await tempProject(t, {
    "package.json": JSON.stringify({ scripts: { build: "vite build", test: "vitest", lint: "eslint ." } }),
    "docker-compose.yml": "services:\n  app:\n    build: .\n",
    Makefile: "pr-check:\n\tnpm test\n"
  });

  const result = await suggestTasks({ projectRoot: project });

  assert.equal(result.ok, true);
  assert.deepEqual(result.suggestions.map((suggestion) => suggestion.label), ["Check", "Test", "Build", "Lint", "Docker"]);
  assert.deepEqual(result.suggestions.map((suggestion) => suggestion.command), [
    "make pr-check",
    "npm run test",
    "npm run build",
    "npm run lint",
    "docker compose build"
  ]);
  assert.equal(result.suggestions.every((suggestion) => suggestion.requiresProject), true);
});

test("suggestTasks dedupes CI fallback when it matches tests", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n"
  });

  const result = await suggestTasks({ projectRoot: project });

  assert.equal(result.ok, true);
  assert.deepEqual(result.suggestions.map((suggestion) => suggestion.command), [
    "go test ./...",
    "go build ./...",
    "go vet ./..."
  ]);
});

test("suggestTasks stays empty until a project is chosen", async () => {
  const result = await suggestTasks({ projectRoot: "" });

  assert.equal(result.ok, true);
  assert.deepEqual(result.suggestions, []);
});

test("planTask preserves exact commands", async () => {
  const result = await planTask({ task: "go test ./...", projectRoot: "" });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "go test ./...");
  assert.equal(result.plan.exact, true);
  assert.equal(result.plan.requiresProject, true);
  assert.equal(classifyIntent("go test ./..."), "exact");
});

test("planTask preserves exact make commands", async () => {
  const result = await planTask({ task: "make pr-check", projectRoot: "" });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.equal(result.plan.requiresProject, true);
  assert.equal(classifyIntent("make pr-check"), "exact");
});

test("planTask asks for a project before planning project work", async () => {
  const result = await planTask({ task: "run tests", projectRoot: "" });

  assert.equal(result.ok, false);
  assert.match(result.error, /Choose a project first/);
  assert.equal(result.actionKind, "choose-project");
  assert.equal(result.actionLabel, "Choose project");
});

test("planTask keeps smoke tests projectless", async () => {
  const result = await planTask({ task: "test connection", projectRoot: "" });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "hostname");
  assert.equal(result.plan.requiresProject, false);
});

test("commandNeedsProject only flags project-style commands", () => {
  assert.equal(commandNeedsProject("go test ./..."), true);
  assert.equal(commandNeedsProject("npm run build"), true);
  assert.equal(commandNeedsProject("make pr-check"), true);
  assert.equal(commandNeedsProject("./scripts/check"), true);
  assert.equal(commandNeedsProject("hostname"), false);
  assert.equal(commandNeedsProject("echo hello"), false);
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
