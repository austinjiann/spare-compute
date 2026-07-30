const fs = require("node:fs/promises");
const path = require("node:path");
const { validatePortableOutputs } = require("./output-path");

async function planTask(request) {
  const task = String(request?.task || "").trim();
  if (!task) {
    return {
      ok: false,
      error: "Enter what you want to do."
    };
  }

  const projectRoot = String(request?.projectRoot || "").trim();
  const profile = await inspectProject(projectRoot);
  const intent = classifyIntent(task);
  const exact = exactCommand(task);
  if (exact && intent === "exact") {
    return plan("Exact command", exact, "This looks like a command already.", profile, {
      exact: true,
      requiresProject: commandNeedsProject(exact)
    });
  }

  const planned = chooseCommand(intent, profile);
  if (planned) {
    return plan(planned.title, planned.command, planned.detail, profile, {
      requiresProject: planned.requiresProject,
      outputs: planned.outputs
    });
  }

  if (projectIntent(intent) && !projectRoot) {
    return {
      ok: false,
      error: "Choose a project first so ComputeHop can pick the right command and send those files to the worker.",
      profile
    };
  }

  if (exact) {
    return plan("Exact command", exact, "No project rule matched, so this will run exactly as typed.", profile, {
      exact: true,
      requiresProject: commandNeedsProject(exact)
    });
  }

  return {
    ok: false,
    error: "I could not turn that into a safe local command yet. Try: run tests, build app, check ci, or type the exact command.",
    profile
  };
}

async function suggestTasks(request) {
  const projectRoot = String(request?.projectRoot || "").trim();
  const profile = await inspectProject(projectRoot);
  if (!projectRoot) {
    return {
      ok: true,
      suggestions: []
    };
  }

  const candidates = [
    { id: "ci", label: "Check", task: "check CI", intent: "ci" },
    { id: "test", label: "Test", task: "run tests", intent: "test" },
    { id: "build", label: "Build", task: "build the app", intent: "build" },
    { id: "package", label: "Package", task: "package the app", intent: "package" },
    { id: "lint", label: "Lint", task: "lint project", intent: "lint" },
    { id: "docker-build", label: "Docker", task: "build docker image", intent: "docker-build" }
  ];
  const seenCommands = new Set();
  const suggestions = [];

  for (const candidate of candidates) {
    const planned = chooseCommand(candidate.intent, profile);
    if (!planned || seenCommands.has(planned.command)) {
      continue;
    }
    seenCommands.add(planned.command);
    suggestions.push({
      id: candidate.id,
      label: candidate.label,
      task: candidate.task,
      title: planned.title,
      command: planned.command,
      detail: planned.detail,
      requiresProject: Boolean(planned.requiresProject),
      outputs: normalizeOutputs(planned.outputs),
      projectRoot,
      detected: detectedLabels(profile)
    });
  }

  return {
    ok: true,
    suggestions
  };
}

async function inspectProject(projectRoot) {
  const root = projectRoot || "";
  const profile = {
    root,
    files: {},
    packageManager: "npm",
    packageScripts: {},
    makeTargets: []
  };
  if (!root) {
    return profile;
  }

  const entries = await Promise.all(
    [
      "package.json",
      "pnpm-lock.yaml",
      "yarn.lock",
      "bun.lock",
      "bun.lockb",
      "go.mod",
      "Package.swift",
      "Cargo.toml",
      "pyproject.toml",
      "pytest.ini",
      "Dockerfile",
      "dockerfile",
      "compose.yaml",
      "compose.yml",
      "docker-compose.yaml",
      "docker-compose.yml",
      "Makefile",
      "makefile"
    ].map(async (name) => [name, await fileExists(path.join(root, name))])
  );
  profile.files = Object.fromEntries(entries);
  profile.packageManager = packageManager(profile.files);

  if (profile.files["package.json"]) {
    try {
      const value = JSON.parse(await fs.readFile(path.join(root, "package.json"), "utf8"));
      profile.packageScripts = value.scripts || {};
    } catch {
      profile.packageScripts = {};
    }
  }

  const makefile = profile.files.Makefile ? "Makefile" : profile.files.makefile ? "makefile" : "";
  if (makefile) {
    try {
      profile.makeTargets = parseMakeTargets(await fs.readFile(path.join(root, makefile), "utf8"));
    } catch {
      profile.makeTargets = [];
    }
  }

  return profile;
}

function chooseCommand(intent, profile) {
  if (intent === "ci") {
    return makeTarget(profile, "pr-check", "Run project checks", "Use the repo's PR validation target.")
      || makeTarget(profile, "check", "Run project checks", "Use the repo's check target.")
      || script(profile, "ci", "Run project checks", "Use the package's CI script.")
      || commandForTests(profile);
  }

  if (intent === "test") {
    return script(profile, "test", "Run tests", "Use the package's test script.")
      || makeTarget(profile, "test", "Run tests", "Use the repo's test target.")
      || commandForTests(profile);
  }

  if (intent === "build") {
    return script(profile, "build", "Build project", "Use the package's build script.")
      || makeTarget(profile, "build", "Build project", "Use the repo's build target.")
      || commandForBuild(profile)
      || commandForDockerBuild(profile);
  }

  if (intent === "package") {
    return makeTarget(profile, "macos-package", "Package macOS app", "Use the repo's macOS app packaging target.", {
      outputs: ["dist/macos/ComputeHop.app"]
    })
      || makeTarget(profile, "package", "Package project", "Use the repo's package target.")
      || makeTarget(profile, "release", "Package project", "Use the repo's release target.")
      || script(profile, "package", "Package project", "Use the package's package script.")
      || script(profile, "release", "Package project", "Use the package's release script.")
      || script(profile, "build", "Build project", "Use the package's build script.")
      || makeTarget(profile, "build", "Build project", "Use the repo's build target.")
      || commandForBuild(profile);
  }

  if (intent === "docker-build") {
    return commandForDockerBuild(profile);
  }

  if (intent === "lint") {
    return script(profile, "lint", "Lint project", "Use the package's lint script.")
      || makeTarget(profile, "lint", "Lint project", "Use the repo's lint target.")
      || makeTarget(profile, "fmt", "Check formatting", "Use the repo's formatting target.")
      || commandForLint(profile);
  }

  if (intent === "install") {
    if (profile.files["package.json"]) {
      return {
        title: "Install dependencies",
        command: `${profile.packageManager} install`,
        detail: `Use ${profile.packageManager} for this JavaScript project.`,
        requiresProject: true
      };
    }
  }

  if (intent === "smoke") {
    return {
      title: "Test connection",
      command: "hostname",
      detail: "Run a tiny command that prints the selected computer's hostname.",
      requiresProject: false
    };
  }

  return null;
}

function commandForTests(profile) {
  if (profile.files["go.mod"]) {
    return { title: "Run Go tests", command: "go test ./...", detail: "Detected go.mod.", requiresProject: true };
  }
  if (profile.files["Package.swift"]) {
    return { title: "Run Swift tests", command: "swift test", detail: "Detected Package.swift.", requiresProject: true };
  }
  if (profile.files["Cargo.toml"]) {
    return { title: "Run Rust tests", command: "cargo test", detail: "Detected Cargo.toml.", requiresProject: true };
  }
  if (profile.files["pyproject.toml"] || profile.files["pytest.ini"]) {
    return { title: "Run Python tests", command: "pytest", detail: "Detected Python project files.", requiresProject: true };
  }
  return null;
}

function commandForBuild(profile) {
  if (profile.files["go.mod"]) {
    return { title: "Build Go project", command: "go build ./...", detail: "Detected go.mod.", requiresProject: true };
  }
  if (profile.files["Package.swift"]) {
    return { title: "Build Swift package", command: "swift build", detail: "Detected Package.swift.", requiresProject: true };
  }
  if (profile.files["Cargo.toml"]) {
    return { title: "Build Rust project", command: "cargo build", detail: "Detected Cargo.toml.", requiresProject: true };
  }
  return null;
}

function commandForDockerBuild(profile) {
  if (hasComposeFile(profile)) {
    return {
      title: "Build containers",
      command: "docker compose build",
      detail: "Detected a Compose file.",
      requiresProject: true
    };
  }
  if (hasDockerfile(profile)) {
    return {
      title: "Build Docker image",
      command: "docker build .",
      detail: "Detected a Dockerfile.",
      requiresProject: true
    };
  }
  return null;
}

function commandForLint(profile) {
  if (profile.files["go.mod"]) {
    return { title: "Vet Go project", command: "go vet ./...", detail: "Detected go.mod.", requiresProject: true };
  }
  if (profile.files["Cargo.toml"]) {
    return { title: "Lint Rust project", command: "cargo clippy", detail: "Detected Cargo.toml.", requiresProject: true };
  }
  if (profile.files["pyproject.toml"]) {
    return { title: "Lint Python project", command: "ruff check .", detail: "Detected pyproject.toml.", requiresProject: true };
  }
  return null;
}

function script(profile, name, title, detail, options = {}) {
  if (!profile.packageScripts[name]) {
    return null;
  }
  return {
    title,
    command: `${profile.packageManager} run ${name}`,
    detail,
    requiresProject: true,
    outputs: normalizeOutputs(options.outputs)
  };
}

function makeTarget(profile, name, title, detail, options = {}) {
  if (!profile.makeTargets.includes(name)) {
    return null;
  }
  return {
    title,
    command: `make ${name}`,
    detail,
    requiresProject: true,
    outputs: normalizeOutputs(options.outputs)
  };
}

function projectIntent(intent) {
  return ["ci", "test", "build", "package", "docker-build", "lint", "install"].includes(intent);
}

function commandNeedsProject(command) {
  const value = String(command || "").trim().toLowerCase();
  return (
    /^\.\//.test(value) ||
    /^make(?:\s|$)/.test(value) ||
    /^(npm|pnpm|yarn|bun)(?:\s|$)/.test(value) ||
    /^go\s+(test|build|run|generate|vet)(?:\s|$)/.test(value) ||
    /^swift\s+(test|build|run|package)(?:\s|$)/.test(value) ||
    /^cargo\s+(test|build|run|check|clippy)(?:\s|$)/.test(value) ||
    /^(pytest|ruff|mypy)(?:\s|$)/.test(value) ||
    /^python\s+(-m\s+)?(pytest|ruff|mypy)(?:\s|$)/.test(value) ||
    /^(uv|poetry)\s+run(?:\s|$)/.test(value) ||
    /^docker\s+(build|compose)(?:\s|$)/.test(value)
  );
}

function classifyIntent(task) {
  const value = task.toLowerCase();
  if (looksLikeCommand(task)) {
    return "exact";
  }
  if (/\b(docker|container|containers|compose)\b/.test(value) && /\b(build|image|images)\b/.test(value)) {
    return "docker-build";
  }
  if (/\b(lint|format|fmt|style)\b/.test(value)) {
    return "lint";
  }
  if (/\b(ci|pr check|preflight|validate|checks?)\b/.test(value)) {
    return "ci";
  }
  if (/\b(hostname|smoke|connection|ping)\b/.test(value)) {
    return "smoke";
  }
  if (/\b(package|release)\b/.test(value)) {
    return "package";
  }
  if (/\b(test|tests|specs?)\b/.test(value)) {
    return "test";
  }
  if (/\b(build|compile|bundle|package)\b/.test(value)) {
    return "build";
  }
  if (/\b(install|deps|dependencies)\b/.test(value)) {
    return "install";
  }
  return "unknown";
}

function exactCommand(task) {
  return looksLikeCommand(task) ? task : "";
}

function looksLikeCommand(task) {
  const value = task.trim();
  return (
    /^make\s+[A-Za-z0-9_.-]+(?:\s|$)/i.test(value) ||
    value.includes("/") ||
    value.includes("./") ||
    value.includes("--") ||
    /^[a-z0-9_.-]+(\s|$)/i.test(value) && !/^(run|build|bundle|package|release|test|check|checks|ci|fix|verify|validate|preflight|lint|format|fmt|style|install|deps|dependencies|smoke|ping|connection|please|can|could|make|do)\b/i.test(value)
  );
}

function packageManager(files) {
  if (files["pnpm-lock.yaml"]) {
    return "pnpm";
  }
  if (files["yarn.lock"]) {
    return "yarn";
  }
  if (files["bun.lock"] || files["bun.lockb"]) {
    return "bun";
  }
  return "npm";
}

function hasDockerfile(profile) {
  return Boolean(profile.files.Dockerfile || profile.files.dockerfile);
}

function hasComposeFile(profile) {
  return Boolean(
    profile.files["compose.yaml"] ||
    profile.files["compose.yml"] ||
    profile.files["docker-compose.yaml"] ||
    profile.files["docker-compose.yml"]
  );
}

function parseMakeTargets(contents) {
  const targets = [];
  for (const line of contents.split(/\r?\n/)) {
    const match = line.match(/^([A-Za-z0-9][A-Za-z0-9_.-]*):(?:\s|$)/);
    if (match && !targets.includes(match[1])) {
      targets.push(match[1]);
    }
  }
  return targets;
}

async function fileExists(filePath) {
  try {
    const info = await fs.stat(filePath);
    return info.isFile();
  } catch {
    return false;
  }
}

function plan(title, command, detail, profile, options = {}) {
  return {
    ok: true,
    plan: {
      title,
      command,
      detail,
      exact: Boolean(options.exact),
      requiresProject: Boolean(options.requiresProject),
      outputs: normalizeOutputs(options.outputs),
      projectRoot: profile.root || "",
      detected: detectedLabels(profile)
    }
  };
}

function normalizeOutputs(outputs) {
  const validated = validatePortableOutputs(outputs);
  return validated.ok ? validated.outputs : [];
}

function detectedLabels(profile) {
  const labels = [];
  if (profile.files["package.json"]) {
    labels.push(`${profile.packageManager} package`);
  }
  if (profile.files["go.mod"]) {
    labels.push("Go");
  }
  if (profile.files["Package.swift"]) {
    labels.push("Swift");
  }
  if (profile.files["Cargo.toml"]) {
    labels.push("Rust");
  }
  if (profile.files["pyproject.toml"] || profile.files["pytest.ini"]) {
    labels.push("Python");
  }
  if (hasDockerfile(profile)) {
    labels.push("Docker");
  }
  if (hasComposeFile(profile)) {
    labels.push("Compose");
  }
  if (profile.makeTargets.length > 0) {
    labels.push("Makefile");
  }
  return labels;
}

module.exports = {
  commandNeedsProject,
  classifyIntent,
  inspectProject,
  planTask,
  suggestTasks
};
