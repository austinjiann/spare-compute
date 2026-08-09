const fs = require("node:fs/promises");
const path = require("node:path");
const capabilityCatalog = require("./capability-catalog");

async function inspectProject(projectRoot) {
  const root = projectRoot || "";
  const profile: any = {
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

function requiredToolIDsForCommand(command, profile: any = {}) {
  const value = String(command || "").trim();
  const makeTarget = value.match(/^make\s+([A-Za-z0-9][A-Za-z0-9_.-]*)(?:\s|$)/)?.[1] || "";
  if (makeTarget && Array.isArray(profile.makeTargets) && profile.makeTargets.includes(makeTarget)) {
    return requiredToolIDsForMakeTarget(profile, makeTarget);
  }
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
  return capabilityCatalog.commonToolIDs();
}

function normalizeToolIDs(values) {
  return capabilityCatalog.normalizeToolIDs(values);
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

function detectedLabels(profile: any = {}) {
  const labels = [];
  const files = profile.files || {};
  if (files["package.json"]) {
    labels.push(`${profile.packageManager || "npm"} package`);
  }
  if (files["go.mod"]) {
    labels.push("Go");
  }
  if (files["Package.swift"]) {
    labels.push("Swift");
  }
  if (files["Cargo.toml"]) {
    labels.push("Rust");
  }
  if (files["pyproject.toml"] || files["pytest.ini"]) {
    labels.push("Python");
  }
  if (Object.entries(files).some(([name, present]) => Boolean(present) && /^Dockerfile(?:\..+)?$/i.test(name))) {
    labels.push("Docker");
  }
  if (Object.entries(files).some(([name, present]) => Boolean(present) && /^(?:docker-)?compose(?:\.[^.]+)?\.ya?ml$/i.test(name))) {
    labels.push("Compose");
  }
  if (Array.isArray(profile.makeTargets) && profile.makeTargets.length > 0) {
    labels.push("Makefile");
  }
  return labels;
}

module.exports = {
  commandNeedsProject,
  detectedLabels,
  inspectProject,
  requiredToolIDsForCommand,
  stripPlacementSuffix,
  targetArchitectureForTask,
  targetPlatformForTask,
  targetPreferenceForTask
};
