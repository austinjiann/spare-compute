const {
  commandNeedsProject,
  stripPlacementSuffix,
  targetArchitectureForTask,
  targetPlatformForTask,
  targetPreferenceForTask
} = require("./planner");
const { splitCommandLine } = require("./command-line");
const { validatePortableOutputs } = require("./output-path");
const { capabilityForCommand } = require("./work-policy");

const defaultOpenAIBaseURL = "https://api.openai.com/v1";
const defaultOpenAIModel = "gpt-5.6";
const plannerTimeoutMs = 20_000;

const planSchema = {
  type: "object",
  additionalProperties: false,
  properties: {
    ok: { type: "boolean" },
    title: { type: "string" },
    command: { type: "string" },
    detail: { type: "string" },
    requiresProject: { type: "boolean" },
    outputs: {
      type: "array",
      maxItems: 64,
      items: { type: "string" }
    },
    capability: {
      type: "string",
      enum: ["", "builds", "tests", "docker", "ai", "video", "commands"]
    }
  },
  required: ["ok", "title", "command", "detail", "requiresProject", "outputs", "capability"]
};

function openAIPlannerConfig(env = process.env) {
  const apiKey = cleanString(env.COMPUTEHOP_AI_API_KEY || env.OPENAI_API_KEY);
  const model = cleanString(env.COMPUTEHOP_AI_MODEL || env.COMPUTEHOP_OPENAI_MODEL || env.OPENAI_MODEL) || defaultOpenAIModel;
  const baseURL = cleanBaseURL(env.COMPUTEHOP_AI_BASE_URL || env.OPENAI_BASE_URL || defaultOpenAIBaseURL);
  return {
    configured: Boolean(apiKey),
    provider: "openai",
    apiKey,
    baseURL,
    model
  };
}

async function planTaskWithOpenAI(request: any = {}, options: any = {}) {
  const config = options.config || openAIPlannerConfig(options.env || process.env);
  if (!config.configured) {
    return { ok: false, error: "Set OPENAI_API_KEY to use the AI planner." };
  }

  const fetchImpl = options.fetchImpl || globalThis.fetch;
  if (typeof fetchImpl !== "function") {
    return { ok: false, error: "This runtime does not provide fetch for the AI planner." };
  }

  const task = cleanString(request.task);
  if (!task) {
    return { ok: false, error: "Enter what you want to do." };
  }

  const profile = request.profile || {};
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), options.timeoutMs || plannerTimeoutMs);
  try {
    const response = await fetchImpl(`${config.baseURL}/responses`, {
      method: "POST",
      headers: {
        "Authorization": `Bearer ${config.apiKey}`,
        "Content-Type": "application/json"
      },
      body: JSON.stringify(openAIPlanRequest({
        model: config.model,
        task,
        projectRoot: request.projectRoot || "",
        profile
      })),
      signal: controller.signal
    });
    if (!response.ok) {
      return {
        ok: false,
        error: `AI planner request failed with HTTP ${response.status}.`
      };
    }

    const data = await response.json();
    return normalizeOpenAIPlan(data, {
      task,
      projectRoot: request.projectRoot || "",
      profile,
      model: config.model
    });
  } catch (error) {
    return {
      ok: false,
      error: error?.name === "AbortError"
        ? "AI planner timed out."
        : `AI planner failed: ${error?.message || "unknown error"}.`
    };
  } finally {
    clearTimeout(timeout);
  }
}

function openAIPlanRequest(request: any = {}) {
  const profile = projectProfileForPrompt(request.profile || {});
  return {
    model: request.model || defaultOpenAIModel,
    input: [
      {
        role: "system",
        content: [
          "You translate a user's plain-language background-compute task into one safe native command for ComputeHop.",
          "Return JSON only through the provided schema.",
          "Use only one executable command. No shell operators, pipes, redirects, backgrounding, command substitution, sudo, destructive deletes, or interactive commands.",
          "Prefer existing package scripts and Makefile targets from the project profile.",
          "If the command produces files the user likely wants back, set outputs to portable relative paths such as dist or report.pdf; otherwise use an empty array.",
          "If the task is unsafe, ambiguous, interactive, or cannot be expressed as one background command, set ok=false and leave command empty.",
          "Do not invent files, credentials, model names, hosts, or environment variables."
        ].join(" ")
      },
      {
        role: "user",
        content: JSON.stringify({
          task: request.task || "",
          projectRootSelected: Boolean(cleanString(request.projectRoot)),
          project: profile
        })
      }
    ],
    text: {
      format: {
        type: "json_schema",
        name: "computehop_planned_command",
        strict: true,
        schema: planSchema
      }
    }
  };
}

function normalizeOpenAIPlan(response, context: any = {}) {
  const text = extractResponseText(response);
  if (!text) {
    return { ok: false, error: "AI planner returned no command plan." };
  }

  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { ok: false, error: "AI planner returned invalid JSON." };
  }

  if (!parsed.ok) {
    return {
      ok: false,
      error: cleanString(parsed.detail) || cleanString(parsed.title) || "AI planner could not make a safe command."
    };
  }

  const targetPreference = targetPreferenceForTask(context.task);
  const targetPlatform = targetPlatformForTask(context.task);
  const targetArchitecture = targetArchitectureForTask(context.task);
  const command = cleanString((targetPreference || targetPlatform || targetArchitecture) ? stripPlacementSuffix(parsed.command) : parsed.command);
  if (!command) {
    return { ok: false, error: "AI planner returned an empty command." };
  }

  const unsafe = unsafeCommandReason(command);
  if (unsafe) {
    return { ok: false, error: `AI planner returned an unsafe command: ${unsafe}.` };
  }

  const inferredCapability = capabilityForCommand(command);
  const capability = normalizeCapability(parsed.capability) || inferredCapability || "commands";
  const requiresProject = Boolean(parsed.requiresProject) || commandNeedsProject(command);
  if (requiresProject && !cleanString(context.projectRoot)) {
    return {
      ok: false,
      error: "Choose a project first so ComputeHop can send those files to the worker.",
      actionKind: "choose-project",
      actionLabel: "Choose project"
    };
  }
  const outputValidation = validatePortableOutputs(Array.isArray(parsed.outputs) ? parsed.outputs : []);
  if (!outputValidation.ok) {
    return {
      ok: false,
      error: `AI planner returned unsafe outputs: ${outputValidation.error}`
    };
  }

  return {
    ok: true,
    plan: {
      title: cleanString(parsed.title) || "AI planned command",
      command,
      detail: cleanString(parsed.detail) || `Planned by ${context.model || "OpenAI"}. Review before running.`,
      exact: capability === "commands",
      requiresProject,
      capability,
      outputs: outputValidation.outputs,
      targetPreference,
      targetPlatform,
      targetArchitecture,
      projectRoot: cleanString(context.projectRoot),
      detected: detectedLabels(context.profile || []),
      planner: "openai"
    }
  };
}

function extractResponseText(response) {
  if (typeof response?.output_text === "string") {
    return response.output_text.trim();
  }
  const output = Array.isArray(response?.output) ? response.output : [];
  for (const item of output) {
    const content = Array.isArray(item?.content) ? item.content : [];
    for (const part of content) {
      if (typeof part?.text === "string") {
        return part.text.trim();
      }
    }
  }
  return "";
}

function projectProfileForPrompt(profile: any = {}) {
  return {
    detected: detectedLabels(profile),
    packageManager: cleanString(profile.packageManager) || "npm",
    packageScripts: Object.keys(profile.packageScripts || {}).sort(),
    makeTargets: Array.isArray(profile.makeTargets) ? [...profile.makeTargets].sort() : [],
    files: Object.fromEntries(
      Object.entries(profile.files || {})
        .filter((entry) => Boolean(entry[1]))
        .map((entry) => [entry[0], true])
    )
  };
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
  if (files.Dockerfile || files.dockerfile) {
    labels.push("Docker");
  }
  if (
    files["compose.yaml"] ||
    files["compose.yml"] ||
    files["docker-compose.yaml"] ||
    files["docker-compose.yml"]
  ) {
    labels.push("Compose");
  }
  if (Array.isArray(profile.makeTargets) && profile.makeTargets.length > 0) {
    labels.push("Makefile");
  }
  return labels;
}

function unsafeCommandReason(command) {
  const value = cleanString(command);
  const parsed = safeSplitCommandLine(value);
  const executable = String(parsed.parts[0] || "").toLowerCase();
  const args = parsed.parts.slice(1).map((part) => String(part || "").toLowerCase());
  if (/[\r\n]/.test(value)) {
    return "multiple lines are not allowed";
  }
  if (parsed.error) {
    return "invalid command quoting is not allowed";
  }
  if (/(^|\s)(sudo|su)(\s|$)/.test(value)) {
    return "privilege escalation is not allowed";
  }
  if (/(^|\s)rm\s+(-[^\s]*[rf][^\s]*|-[^\s]*[fr][^\s]*)(\s|$)/.test(value)) {
    return "recursive or forced removal is not allowed";
  }
  if (
    ["bash", "sh", "zsh", "fish", "pwsh", "powershell", "powershell.exe"].includes(executable) &&
    args.some((arg) => arg === "-c" || arg === "-lc" || arg === "-command")
  ) {
    return "shell wrapper commands are not allowed";
  }
  if ((executable === "cmd" || executable === "cmd.exe") && args.includes("/c")) {
    return "shell wrapper commands are not allowed";
  }
  if (["ssh", "vim", "vi", "nano", "emacs", "less", "more", "top", "htop"].includes(executable)) {
    return "interactive commands are not allowed";
  }
  if (executable === "tail" && args.includes("-f")) {
    return "interactive commands are not allowed";
  }
  if (/[;&|<>`]/.test(value) || /\$\(/.test(value)) {
    return "shell operators are not allowed";
  }
  return "";
}

function safeSplitCommandLine(command) {
  try {
    return { parts: splitCommandLine(command), error: "" };
  } catch (error) {
    return { parts: [], error: error?.message || "invalid command" };
  }
}

function normalizeCapability(value) {
  const capability = cleanString(value);
  return ["builds", "tests", "docker", "ai", "video", "commands"].includes(capability) ? capability : "";
}

function cleanBaseURL(value) {
  return (cleanString(value) || defaultOpenAIBaseURL).replace(/\/+$/, "");
}

function cleanString(value) {
  return String(value || "").trim();
}

module.exports = {
  defaultOpenAIModel,
  extractResponseText,
  normalizeOpenAIPlan,
  openAIPlannerConfig,
  openAIPlanRequest,
  planTaskWithOpenAI,
  unsafeCommandReason
};
