const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  classifyIntent,
  commandNeedsProject,
  planTask,
  stripPlacementSuffix,
  suggestTasks,
  targetArchitectureForTask,
  targetPlatformForTask,
  targetPreferenceForTask
} = require("./planner");

test("planTask prefers Makefile PR check for CI", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n",
    Makefile: "pr-check:\n\tgo test ./...\n"
  });

  const result = await planTask({ task: "fix ci", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.equal(result.plan.requiresProject, true);
  assert.deepEqual(result.plan.requiredToolIDs, ["go", "make"]);
  assert.deepEqual(result.plan.detected.sort(), ["Go", "Makefile"].sort());
  assert.equal(classifyIntent("fix ci"), "ci");
  assert.equal(classifyIntent("validate project"), "ci");
  assert.equal(classifyIntent("preflight"), "ci");
});

test("planTask carries remote placement hints from natural language", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n"
  });

  const remote = await planTask({ task: "run tests on the other computer", projectRoot: project });
  const local = await planTask({ task: "run tests here", projectRoot: project });

  assert.equal(remote.ok, true);
  assert.equal(remote.plan.command, "go test ./...");
  assert.equal(remote.plan.targetPreference, "worker");
  assert.equal(local.ok, true);
  assert.equal(local.plan.targetPreference, "local");
  const windows = await planTask({ task: "run tests on Windows", projectRoot: project });
  const mac = await planTask({ task: "run tests on macOS", projectRoot: project });
  const appleSilicon = await planTask({ task: "run tests on Apple Silicon", projectRoot: project });
  const arm = await planTask({ task: "run tests on arm64", projectRoot: project });
  assert.equal(windows.plan.targetPreference, "worker");
  assert.equal(windows.plan.targetPlatform, "windows");
  assert.equal(mac.plan.targetPreference, "");
  assert.equal(mac.plan.targetPlatform, "darwin");
  assert.equal(appleSilicon.plan.targetPlatform, "darwin");
  assert.equal(appleSilicon.plan.targetArchitecture, "arm64");
  assert.equal(arm.plan.targetArchitecture, "arm64");
  assert.equal(targetPreferenceForTask("delegate the build to the gaming pc"), "worker");
  assert.equal(targetPreferenceForTask("run this on this Mac"), "local");
  assert.equal(targetPlatformForTask("delegate the build to Windows"), "windows");
  assert.equal(targetPlatformForTask("build on Linux"), "linux");
  assert.equal(targetPlatformForTask("run this on this Mac"), "darwin");
  assert.equal(targetArchitectureForTask("run tests on Apple Silicon"), "arm64");
  assert.equal(targetArchitectureForTask("build on x64"), "amd64");
});

test("planTask strips placement words from exact commands", async () => {
  const remoteGo = await planTask({ task: "go test ./... on the worker", projectRoot: "" });
  const remoteMake = await planTask({ task: "make pr-check on the other computer", projectRoot: "" });
  const remoteUtility = await planTask({ task: "hostname on the worker", projectRoot: "" });
  const remoteWindows = await planTask({ task: "hostname on Windows", projectRoot: "" });
  const remoteArm = await planTask({ task: "hostname on arm64", projectRoot: "" });
  const localGo = await planTask({ task: "go test ./... here", projectRoot: "" });
  const localMake = await planTask({ task: "make pr-check locally", projectRoot: "" });

  assert.equal(remoteGo.ok, true);
  assert.equal(remoteGo.plan.command, "go test ./...");
  assert.equal(remoteGo.plan.targetPreference, "worker");
  assert.equal(remoteGo.plan.requiresProject, true);
  assert.equal(remoteMake.plan.command, "make pr-check");
  assert.equal(remoteMake.plan.targetPreference, "worker");
  assert.equal(remoteUtility.plan.command, "hostname");
  assert.equal(remoteUtility.plan.requiresProject, false);
  assert.equal(remoteWindows.plan.command, "hostname");
  assert.equal(remoteWindows.plan.targetPreference, "worker");
  assert.equal(remoteWindows.plan.targetPlatform, "windows");
  assert.equal(remoteArm.plan.command, "hostname");
  assert.equal(remoteArm.plan.targetArchitecture, "arm64");
  assert.equal(localGo.plan.command, "go test ./...");
  assert.equal(localGo.plan.targetPreference, "local");
  assert.equal(localMake.plan.command, "make pr-check");
  assert.equal(localMake.plan.targetPreference, "local");
  assert.equal(localMake.plan.requiresProject, true);
  assert.equal(stripPlacementSuffix("pnpm test on the worker"), "pnpm test");
});

test("planTask preserves literal local words in exact command arguments", async () => {
  const echo = await planTask({ task: "echo here", projectRoot: "" });
  const python = await planTask({ task: "python script.py --arg here", projectRoot: "" });
  const printf = await planTask({ task: "printf locally", projectRoot: "" });
  const echoWindows = await planTask({ task: "echo on Windows", projectRoot: "" });
  const echoArm = await planTask({ task: "echo on arm64", projectRoot: "" });

  assert.equal(echo.ok, true);
  assert.equal(echo.plan.command, "echo here");
  assert.equal(echo.plan.targetPreference, "");
  assert.equal(echo.plan.requiresProject, false);
  assert.equal(python.ok, true);
  assert.equal(python.plan.command, "python script.py --arg here");
  assert.equal(python.plan.targetPreference, "");
  assert.equal(printf.ok, true);
  assert.equal(printf.plan.command, "printf locally");
  assert.equal(printf.plan.targetPreference, "");
  assert.equal(stripPlacementSuffix("echo here"), "echo here");
  assert.equal(echoWindows.plan.command, "echo on Windows");
  assert.equal(echoWindows.plan.targetPlatform, "");
  assert.equal(echoArm.plan.command, "echo on arm64");
  assert.equal(echoArm.plan.targetArchitecture, "");
  assert.equal(targetPreferenceForTask("echo here"), "");
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
  assert.deepEqual(build.plan.requiredToolIDs, ["node", "pnpm"]);
  assert.deepEqual(tests.plan.requiredToolIDs, ["node", "pnpm"]);
});

test("planTask infers common tools from Makefile prerequisites", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n",
    Makefile: [
      "pr-check: test deploy-check",
      "test:",
      "\tgo test ./...",
      "deploy-check:",
      "\tdocker compose config --quiet"
    ].join("\n")
  });

  const result = await planTask({ task: "check ci", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.deepEqual(result.plan.requiredToolIDs, ["docker", "go", "make"]);
});

test("planTask detects Python tools referenced by Makefile recipes", async (t) => {
  const project = await tempProject(t, {
    "pyproject.toml": "[project]\nname = \"app\"\n",
    Makefile: [
      "pr-check: test lint",
      "test:",
      "\tuv run pytest",
      "lint:",
      "\truff check ."
    ].join("\n")
  });

  const result = await planTask({ task: "check ci", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.deepEqual(result.plan.requiredToolIDs, ["make", "pytest", "python3", "ruff", "uv"]);
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
  assert.equal(result.plan.targetPlatform, "darwin");
  assert.deepEqual(result.plan.requiredToolIDs, ["go", "make"]);
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
  assert.deepEqual(suggestion.requiredToolIDs, ["make"]);
  assert.deepEqual(suggestion.outputs, [
    "dist/macos/ComputeHop-macos.zip",
    "dist/macos/ComputeHop-macos.zip.sha256"
  ]);
  assert.equal(suggestion.targetPlatform, "darwin");
});

test("planTask maps Swift package tests", async (t) => {
  const project = await tempProject(t, {
    "Package.swift": "// swift-tools-version: 6.0\n"
  });

  const result = await planTask({ task: "run tests", projectRoot: project });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "swift test");
  assert.equal(result.plan.requiresProject, true);
  assert.deepEqual(result.plan.requiredToolIDs, ["swift"]);
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
  assert.deepEqual(result.plan.requiredToolIDs, ["docker"]);
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
