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
  const targetPreference = targetPreferenceForTask(task);
  const targetPlatform = targetPlatformForTask(task);
  const targetArchitecture = targetArchitectureForTask(task);
  if (exact && intent === "exact") {
    return plan("Exact command", exact, "This looks like a command already.", profile, {
      exact: true,
      requiresProject: commandNeedsProject(exact),
      targetPreference,
      targetPlatform,
      targetArchitecture
    });
  }

  const planned = chooseCommand(intent, profile);
  if (planned) {
    return plan(planned.title, planned.command, planned.detail, profile, {
      requiresProject: planned.requiresProject,
      outputs: planned.outputs,
      requiredToolIDs: planned.requiredToolIDs,
      targetPreference,
      targetPlatform: targetPlatform || planned.targetPlatform,
      targetArchitecture
    });
  }

  if (projectIntent(intent) && !projectRoot) {
    return {
      ok: false,
      error: "Choose a project first so ComputeHop can pick the right command and send those files to the worker.",
      actionKind: "choose-project",
      actionLabel: "Choose project",
      profile
    };
  }

  if (exact) {
    return plan("Exact command", exact, "No project rule matched, so this will run exactly as typed.", profile, {
      exact: true,
      requiresProject: commandNeedsProject(exact),
      targetPreference,
      targetPlatform,
      targetArchitecture
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
      requiredToolIDs: normalizeToolIDs(planned.requiredToolIDs),
      targetPlatform: cleanTargetPlatform(planned.targetPlatform),
      targetArchitecture: cleanTargetArchitecture(planned.targetArchitecture),
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
    makeTargets: [],
    makeRecipes: {},
    makePrerequisites: {}
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
      const makefileProfile = parseMakefile(await fs.readFile(path.join(root, makefile), "utf8"));
      profile.makeTargets = makefileProfile.targets;
      profile.makeRecipes = makefileProfile.recipes;
      profile.makePrerequisites = makefileProfile.prerequisites;
    } catch {
      profile.makeTargets = [];
      profile.makeRecipes = {};
      profile.makePrerequisites = {};
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
    return makeTarget(profile, "macos-archive", "Package macOS app", "Create the repo's copyable macOS app archive.", {
      targetPlatform: "darwin",
      outputs: ["dist/macos/ComputeHop-macos.zip", "dist/macos/ComputeHop-macos.zip.sha256"]
    })
      || makeTarget(profile, "macos-package", "Package macOS app", "Use the repo's macOS app packaging target.", {
        targetPlatform: "darwin",
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
        requiresProject: true,
        requiredToolIDs: packageManagerToolIDs(profile.packageManager)
      };
    }
  }

  if (intent === "smoke") {
    return {
      title: "Test connection",
      command: "hostname",
      detail: "Run a tiny command that prints the selected computer's hostname.",
      requiresProject: false,
      requiredToolIDs: ["hostname"]
    };
  }

  return null;
}

function commandForTests(profile) {
  if (profile.files["go.mod"]) {
    return { title: "Run Go tests", command: "go test ./...", detail: "Detected go.mod.", requiresProject: true, requiredToolIDs: ["go"] };
  }
  if (profile.files["Package.swift"]) {
    return { title: "Run Swift tests", command: "swift test", detail: "Detected Package.swift.", requiresProject: true, requiredToolIDs: ["swift"] };
  }
  if (profile.files["Cargo.toml"]) {
    return { title: "Run Rust tests", command: "cargo test", detail: "Detected Cargo.toml.", requiresProject: true, requiredToolIDs: ["cargo"] };
  }
  if (profile.files["pyproject.toml"] || profile.files["pytest.ini"]) {
    return { title: "Run Python tests", command: "pytest", detail: "Detected Python project files.", requiresProject: true, requiredToolIDs: ["pytest"] };
  }
  return null;
}

function commandForBuild(profile) {
  if (profile.files["go.mod"]) {
    return { title: "Build Go project", command: "go build ./...", detail: "Detected go.mod.", requiresProject: true, requiredToolIDs: ["go"] };
  }
  if (profile.files["Package.swift"]) {
    return { title: "Build Swift package", command: "swift build", detail: "Detected Package.swift.", requiresProject: true, requiredToolIDs: ["swift"] };
  }
  if (profile.files["Cargo.toml"]) {
    return { title: "Build Rust project", command: "cargo build", detail: "Detected Cargo.toml.", requiresProject: true, requiredToolIDs: ["cargo"] };
  }
  return null;
}

function commandForDockerBuild(profile) {
  if (hasComposeFile(profile)) {
    return {
      title: "Build containers",
      command: "docker compose build",
      detail: "Detected a Compose file.",
      requiresProject: true,
      requiredToolIDs: ["docker"]
    };
  }
  if (hasDockerfile(profile)) {
    return {
      title: "Build Docker image",
      command: "docker build .",
      detail: "Detected a Dockerfile.",
      requiresProject: true,
      requiredToolIDs: ["docker"]
    };
  }
  return null;
}

function commandForLint(profile) {
  if (profile.files["go.mod"]) {
    return { title: "Vet Go project", command: "go vet ./...", detail: "Detected go.mod.", requiresProject: true, requiredToolIDs: ["go"] };
  }
  if (profile.files["Cargo.toml"]) {
    return { title: "Lint Rust project", command: "cargo clippy", detail: "Detected Cargo.toml.", requiresProject: true, requiredToolIDs: ["cargo"] };
  }
  if (profile.files["pyproject.toml"]) {
    return { title: "Lint Python project", command: "ruff check .", detail: "Detected pyproject.toml.", requiresProject: true, requiredToolIDs: ["ruff"] };
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
    outputs: normalizeOutputs(options.outputs),
    requiredToolIDs: normalizeToolIDs(options.requiredToolIDs || packageManagerToolIDs(profile.packageManager)),
    targetPlatform: cleanTargetPlatform(options.targetPlatform),
    targetArchitecture: cleanTargetArchitecture(options.targetArchitecture)
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
    outputs: normalizeOutputs(options.outputs),
    requiredToolIDs: normalizeToolIDs(options.requiredToolIDs || requiredToolIDsForMakeTarget(profile, name)),
    targetPlatform: cleanTargetPlatform(options.targetPlatform),
    targetArchitecture: cleanTargetArchitecture(options.targetArchitecture)
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

function targetPreferenceForTask(task) {
  const value = String(task || "").trim().toLowerCase();
  if (!value) {
    return "";
  }
  if (looksLikeCommand(task)) {
    return placementSuffixForCommand(task).targetPreference;
  }
  if (
    /\b(here|locally)\b/.test(value) ||
    /\b(on|using|with)\s+(this\s+mac|this\s+computer|my\s+mac|local(?:ly)?|here)\b/.test(value)
  ) {
    return "local";
  }
  if (targetPlatformPreference(value)) {
    return "worker";
  }
  if (
    /\b(on|using|with|to)\s+(the\s+)?(worker|remote|other\s+computer|another\s+computer|desktop|pc|gaming\s+pc|server|home\s+server)\b/.test(value) ||
    /\b(offload|send|delegate)\b/.test(value) && /\b(worker|remote|computer|desktop|pc|server|there)\b/.test(value)
  ) {
    return "worker";
  }
  return "";
}

function targetPlatformForTask(task) {
  const value = String(task || "").trim();
  if (!value) {
    return "";
  }
  if (looksLikeCommand(task)) {
    return placementSuffixForCommand(task).targetPlatform;
  }
  return targetPlatformMention(value.toLowerCase()) || platformForArchitectureMention(value.toLowerCase());
}

function targetArchitectureForTask(task) {
  const value = String(task || "").trim();
  if (!value) {
    return "";
  }
  if (looksLikeCommand(task)) {
    return placementSuffixForCommand(task).targetArchitecture;
  }
  return targetArchitectureMention(value.toLowerCase());
}

function exactCommand(task) {
  return looksLikeCommand(task) ? stripPlacementSuffix(task) : "";
}

function looksLikeCommand(task) {
  const value = task.trim();
  return (
    /^make\s+[A-Za-z0-9_.-]+(?:\s|$)/i.test(value) ||
    value.includes("/") ||
    value.includes("./") ||
    value.includes("--") ||
    /^[a-z0-9_.-]+(\s|$)/i.test(value) && !/^(run|build|bundle|package|release|test|check|checks|ci|fix|verify|validate|preflight|lint|format|fmt|style|install|deps|dependencies|smoke|ping|connection|please|can|could|make|do|send|delegate|offload)\b/i.test(value)
  );
}

function stripPlacementSuffix(task) {
  return placementSuffixForCommand(task).command;
}

function placementSuffixForCommand(task) {
  const value = String(task || "").trim();
  if (!value) {
    return {
      command: "",
      targetPreference: "",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }

  const local = value.replace(/\s+(?:on|using|with)\s+(?:this\s+mac|this\s+computer|my\s+mac|local(?:ly)?|here)$/i, "").trim();
  if (local !== value) {
    return {
      command: local,
      targetPreference: "local",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }

  const platform = placementPlatformSuffix(value);
  if (platform.command !== value) {
    return platform;
  }

  const architecture = placementArchitectureSuffix(value);
  if (architecture.command !== value) {
    return architecture;
  }

  const worker = value.replace(/\s+(?:on|using|with|to)\s+(?:the\s+)?(?:worker|remote|other\s+computer|another\s+computer|desktop|pc|gaming\s+pc|server|home\s+server)$/i, "").trim();
  if (worker !== value) {
    return {
      command: worker,
      targetPreference: "worker",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }

  const bareLocal = value.replace(/\s+(?:here|locally)$/i, "").trim();
  if (bareLocal !== value && commandNeedsProject(bareLocal)) {
    return {
      command: bareLocal,
      targetPreference: "local",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }

  return {
    command: value,
    targetPreference: "",
    targetPlatform: "",
    targetArchitecture: ""
  };
}

function placementPlatformSuffix(value) {
  const match = String(value || "").match(/\s+(?:on|using|with|to)\s+(?:a\s+|the\s+|my\s+)?(windows(?:\s+pc)?|linux(?:\s+server)?|macos|macbook|mac\s+mini|mac\s+studio|mac)$/i);
  if (!match) {
    return {
      command: value,
      targetPreference: "",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }
  const command = value.slice(0, match.index).trim();
  if (!canStripPlacementFromCommand(command)) {
    return {
      command: value,
      targetPreference: "",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }
  return {
    command,
    targetPreference: targetPlatformPreference(match[1]),
    targetPlatform: targetPlatformFromPhrase(match[1]),
    targetArchitecture: ""
  };
}

function placementArchitectureSuffix(value) {
  const match = String(value || "").match(/\s+(?:on|using|with|to)\s+(?:a\s+|the\s+|my\s+)?(apple\s+silicon|arm64|aarch64|x64|amd64|x86_64|x86-64|intel(?:\s+mac)?|intel)$/i);
  if (!match) {
    return {
      command: value,
      targetPreference: "",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }
  const command = value.slice(0, match.index).trim();
  if (!canStripPlacementFromCommand(command)) {
    return {
      command: value,
      targetPreference: "",
      targetPlatform: "",
      targetArchitecture: ""
    };
  }
  return {
    command,
    targetPreference: "",
    targetPlatform: platformForArchitecturePhrase(match[1]),
    targetArchitecture: targetArchitectureFromPhrase(match[1])
  };
}

function canStripPlacementFromCommand(command) {
  return commandNeedsProject(command) || /^(hostname|uname|whoami|date|pwd)(?:\s|$)/i.test(command);
}

function targetPlatformMention(value) {
  const text = String(value || "").trim().toLowerCase();
  const match = text.match(/\b(?:on|using|with|to)\s+(?:a\s+|the\s+|my\s+)?(windows(?:\s+pc)?|linux(?:\s+server)?|this\s+mac|my\s+mac|macos|macbook|mac\s+mini|mac\s+studio|mac)\b/);
  return match ? targetPlatformFromPhrase(match[1]) : "";
}

function targetArchitectureMention(value) {
  const text = String(value || "").trim().toLowerCase();
  const match = text.match(/\b(?:on|using|with|to)\s+(?:a\s+|the\s+|my\s+)?(apple\s+silicon|arm64|aarch64|x64|amd64|x86_64|x86-64|intel(?:\s+mac)?|intel)\b/);
  return match ? targetArchitectureFromPhrase(match[1]) : "";
}

function platformForArchitectureMention(value) {
  const text = String(value || "").trim().toLowerCase();
  const match = text.match(/\b(?:on|using|with|to)\s+(?:a\s+|the\s+|my\s+)?(apple\s+silicon|intel\s+mac)\b/);
  return match ? platformForArchitecturePhrase(match[1]) : "";
}

function targetPlatformPreference(value) {
  const phrase = String(value || "").trim().toLowerCase();
  if (/windows|linux|mac\s+mini|mac\s+studio/.test(phrase)) {
    return "worker";
  }
  return "";
}

function platformForArchitecturePhrase(value) {
  const phrase = String(value || "").trim().toLowerCase();
  if (phrase === "apple silicon" || phrase === "intel mac") {
    return "darwin";
  }
  return "";
}

function targetArchitectureFromPhrase(value) {
  const phrase = String(value || "").trim().toLowerCase();
  if (["apple silicon", "arm64", "aarch64"].includes(phrase)) {
    return "arm64";
  }
  if (["x64", "amd64", "x86_64", "x86-64", "intel", "intel mac"].includes(phrase)) {
    return "amd64";
  }
  return "";
}

function targetPlatformFromPhrase(value) {
  const phrase = String(value || "").trim().toLowerCase();
  if (phrase.startsWith("windows")) {
    return "windows";
  }
  if (phrase.startsWith("linux")) {
    return "linux";
  }
  if (phrase.includes("mac")) {
    return "darwin";
  }
  return "";
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

function parseMakefile(contents) {
  const targets = [];
  const recipes = {};
  const prerequisites = {};
  let currentTarget = "";
  for (const line of contents.split(/\r?\n/)) {
    const match = line.match(/^([A-Za-z0-9][A-Za-z0-9_.-]*):(?:\s+(.*)|$)/);
    if (match && !targets.includes(match[1])) {
      targets.push(match[1]);
    }
    if (match) {
      currentTarget = match[1];
      recipes[currentTarget] = recipes[currentTarget] || [];
      prerequisites[currentTarget] = String(match[2] || "")
        .split(/\s+/)
        .map((value) => value.trim())
        .filter((value) => /^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(value));
      continue;
    }
    if (/^\t/.test(line) && currentTarget) {
      recipes[currentTarget].push(line.replace(/^\t/, "").trim());
      continue;
    }
    if (line.trim() && !/^\s/.test(line)) {
      currentTarget = "";
    }
  }
  return { targets, recipes, prerequisites };
}

function requiredToolIDsForMakeTarget(profile, target) {
  const required = new Set(["make"]);
  const seen = new Set();
  const visit = (name, depth = 0) => {
    if (!name || seen.has(name) || depth > 32) {
      return;
    }
    seen.add(name);
    for (const line of profile.makeRecipes?.[name] || []) {
      for (const toolID of requiredToolIDsForRecipeLine(line)) {
        required.add(toolID);
      }
    }
    for (const dependency of profile.makePrerequisites?.[name] || []) {
      if (profile.makeRecipes?.[dependency] || profile.makePrerequisites?.[dependency]) {
        visit(dependency, depth + 1);
      }
    }
  };
  visit(target);
  for (const toolID of projectToolIDs(profile)) {
    required.add(toolID);
  }
  return normalizeToolIDs([...required]);
}

function requiredToolIDsForRecipeLine(line) {
  const text = String(line || "").trim().replace(/^[@+-]+/, "").trim().toLowerCase();
  if (!text || text.startsWith("#")) {
    return [];
  }
  const required = new Set();
  for (const toolID of commonToolIDs()) {
    const expression = new RegExp(`(?:^|[^a-z0-9_.-])${escapeRegExp(toolID)}(?:$|[^a-z0-9_.-])`, "i");
    if (expression.test(text)) {
      required.add(toolID);
    }
  }
  if (/\bnpm\s+run\b/.test(text)) {
    required.add("node");
    required.add("npm");
  }
  if (/\b(pnpm|yarn|bun)\s+(run|install|test|build)\b/.test(text)) {
    const manager = text.match(/\b(pnpm|yarn|bun)\b/)?.[1] || "";
    for (const toolID of packageManagerToolIDs(manager)) {
      required.add(toolID);
    }
  }
  if (/\bdocker\s+compose\b/.test(text)) {
    required.add("docker");
  }
  return normalizeToolIDs([...required]);
}

function projectToolIDs(profile) {
  const required = [];
  if (profile.files["go.mod"]) {
    required.push("go");
  }
  if (profile.files["Package.swift"]) {
    required.push("swift");
  }
  if (profile.files["Cargo.toml"]) {
    required.push("cargo");
  }
  if (profile.files["package.json"]) {
    required.push(...packageManagerToolIDs(profile.packageManager));
  }
  if (profile.files["pyproject.toml"] || profile.files["pytest.ini"]) {
    required.push("python3");
  }
  if (hasDockerfile(profile) || hasComposeFile(profile)) {
    required.push("docker");
  }
  return normalizeToolIDs(required);
}

function packageManagerToolIDs(packageManagerID) {
  const manager = String(packageManagerID || "").trim().toLowerCase();
  if (!manager) {
    return [];
  }
  if (manager === "npm") {
    return ["node", "npm"];
  }
  if (["pnpm", "yarn", "bun"].includes(manager)) {
    return ["node", manager];
  }
  return [manager];
}

function requiredToolIDsForCommand(command) {
  const value = String(command || "").trim();
  const executable = value.match(/^"([^"]+)"|'([^']+)'|(\S+)/)?.slice(1).find(Boolean) || "";
  const toolID = executable
    .replace(/\\/g, "/")
    .split("/")
    .filter(Boolean)
    .pop()
    ?.replace(/\.(exe|cmd|bat)$/i, "")
    .toLowerCase() || "";
  if (!toolID || executable.startsWith("./") || executable.startsWith("../")) {
    return [];
  }
  if (/^docker\s+compose(?:\s|$)/i.test(value)) {
    return ["docker"];
  }
  if (["npm", "pnpm", "yarn", "bun"].includes(toolID)) {
    return packageManagerToolIDs(toolID);
  }
  return [toolID];
}

function commonToolIDs() {
  return [
    "blender",
    "bun",
    "cargo",
    "docker",
    "docker-compose",
    "ffmpeg",
    "go",
    "make",
    "node",
    "npm",
    "ollama",
    "pnpm",
    "podman",
    "python",
    "python3",
    "ruff",
    "sh",
    "swift",
    "xcodebuild",
    "yarn"
  ];
}

function normalizeToolIDs(values) {
  if (!Array.isArray(values)) {
    return [];
  }
  const seen = new Set();
  return values
    .map((value) => String(value || "").trim().toLowerCase())
    .filter((value) => value && !/\s|=/.test(value))
    .sort()
    .filter((value) => {
      if (seen.has(value)) {
        return false;
      }
      seen.add(value);
      return true;
    });
}

function escapeRegExp(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
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
      requiredToolIDs: normalizeToolIDs(options.requiredToolIDs || requiredToolIDsForCommand(command)),
      targetPreference: cleanTargetPreference(options.targetPreference),
      targetPlatform: cleanTargetPlatform(options.targetPlatform),
      targetArchitecture: cleanTargetArchitecture(options.targetArchitecture),
      projectRoot: profile.root || "",
      detected: detectedLabels(profile)
    }
  };
}

function cleanTargetPreference(value) {
  const target = String(value || "").trim().toLowerCase();
  return target === "worker" || target === "local" ? target : "";
}

function cleanTargetPlatform(value) {
  const target = String(value || "").trim().toLowerCase();
  if (["darwin", "mac", "macos", "osx"].includes(target)) {
    return "darwin";
  }
  if (["windows", "win32", "win"].includes(target)) {
    return "windows";
  }
  if (target === "linux") {
    return "linux";
  }
  return "";
}

function cleanTargetArchitecture(value) {
  const target = String(value || "").trim().toLowerCase();
  if (["arm64", "aarch64", "apple-silicon", "apple silicon"].includes(target)) {
    return "arm64";
  }
  if (["amd64", "x64", "x86_64", "x86-64", "intel"].includes(target)) {
    return "amd64";
  }
  return "";
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
  stripPlacementSuffix,
  suggestTasks,
  targetArchitectureForTask,
  targetPlatformForTask,
  targetPreferenceForTask
};
