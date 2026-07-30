const assert = require("node:assert/strict");
const test = require("node:test");
const {
  extractResponseText,
  normalizeOpenAIPlan,
  openAIPlannerConfig,
  openAIPlanRequest,
  planTaskWithOpenAI,
  unsafeCommandReason
} = require("./openai-planner");

test("openAIPlannerConfig is opt-in through environment", () => {
  assert.equal(openAIPlannerConfig({}).configured, false);
  assert.deepEqual(openAIPlannerConfig({
    OPENAI_API_KEY: " key ",
    COMPUTEHOP_OPENAI_MODEL: " custom-model ",
    OPENAI_BASE_URL: " https://example.com/v1/ "
  }), {
    configured: true,
    apiKey: "key",
    baseURL: "https://example.com/v1",
    model: "custom-model"
  });
});

test("openAIPlanRequest asks for structured one-command JSON without file contents", () => {
  const request = openAIPlanRequest({
    model: "test-model",
    task: "run the app checks",
    projectRoot: "/project",
    profile: {
      files: {
        "package.json": true,
        "secret.txt": false,
        Makefile: true
      },
      packageManager: "pnpm",
      packageScripts: { test: "vitest", build: "vite build" },
      makeTargets: ["pr-check"]
    }
  });

  assert.equal(request.model, "test-model");
  assert.equal(request.text.format.type, "json_schema");
  assert.equal(request.text.format.strict, true);
  assert.deepEqual(request.text.format.schema.required, [
    "ok",
    "title",
    "command",
    "detail",
    "requiresProject",
    "outputs",
    "capability"
  ]);
  const prompt = JSON.parse(request.input[1].content);
  assert.equal(prompt.projectRootSelected, true);
  assert.deepEqual(prompt.project.packageScripts, ["build", "test"]);
  assert.deepEqual(prompt.project.makeTargets, ["pr-check"]);
  assert.deepEqual(prompt.project.files, {
    "package.json": true,
    Makefile: true
  });
});

test("extractResponseText reads Responses API text output shapes", () => {
  assert.equal(extractResponseText({ output_text: " hello " }), "hello");
  assert.equal(extractResponseText({
    output: [{
      content: [{ type: "output_text", text: " nested " }]
    }]
  }), "nested");
});

test("normalizeOpenAIPlan returns a reviewed ComputeHop plan", () => {
  const result = normalizeOpenAIPlan({
    output_text: JSON.stringify({
      ok: true,
      title: "Run custom helper",
      command: "./scripts/check",
      detail: "Runs the project helper.",
      requiresProject: false,
      outputs: ["dist/report.json"],
      capability: ""
    })
  }, {
    projectRoot: "/project",
    model: "test-model",
    profile: {
      files: { "package.json": true },
      packageManager: "npm"
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "./scripts/check");
  assert.equal(result.plan.requiresProject, true);
  assert.equal(result.plan.capability, "commands");
  assert.equal(result.plan.exact, true);
  assert.deepEqual(result.plan.outputs, ["dist/report.json"]);
  assert.equal(result.plan.planner, "openai");
  assert.deepEqual(result.plan.detected, ["npm package"]);
});

test("normalizeOpenAIPlan infers known work capability from the command", () => {
  const result = normalizeOpenAIPlan({
    output_text: JSON.stringify({
      ok: true,
      title: "Run tests",
      command: "go test ./...",
      detail: "Detected a Go test command.",
      requiresProject: true,
      outputs: [],
      capability: ""
    })
  }, {
    projectRoot: "/project"
  });

  assert.equal(result.ok, true);
  assert.equal(result.plan.capability, "tests");
  assert.equal(result.plan.exact, false);
  assert.equal(result.plan.requiresProject, true);
});

test("normalizeOpenAIPlan rejects unsafe or unusable plans", () => {
  assert.match(
    normalizeOpenAIPlan({
      output_text: JSON.stringify({
        ok: true,
        title: "Empty",
        command: "",
        detail: "",
        requiresProject: false,
        outputs: [],
        capability: "commands"
      })
    }).error,
    /empty command/
  );
  assert.match(
    normalizeOpenAIPlan({
      output_text: JSON.stringify({
        ok: true,
        title: "Delete",
        command: "rm -rf dist",
        detail: "delete",
        requiresProject: false,
        outputs: [],
        capability: "commands"
      })
    }).error,
    /unsafe command/
  );
  assert.match(
    normalizeOpenAIPlan({
      output_text: JSON.stringify({
        ok: true,
        title: "Test",
        command: "go test ./...",
        detail: "test",
        requiresProject: true,
        outputs: [],
        capability: "tests"
      })
    }).error,
    /Choose a project first/
  );
  assert.match(
    normalizeOpenAIPlan({
      output_text: JSON.stringify({
        ok: true,
        title: "Package app",
        command: "make package",
        detail: "package",
        requiresProject: true,
        outputs: ["../dist"],
        capability: "builds"
      })
    }, {
      projectRoot: "/project"
    }).error,
    /unsafe outputs/
  );
});

test("unsafeCommandReason catches shell features the native runner does not support", () => {
  assert.match(unsafeCommandReason("sudo make install"), /privilege/);
  assert.match(unsafeCommandReason("rm -rf dist"), /removal/);
  assert.match(unsafeCommandReason("go test ./... && say done"), /shell operators/);
  assert.match(unsafeCommandReason("echo hi | cat"), /shell operators/);
  assert.match(unsafeCommandReason("echo $(whoami)"), /shell operators/);
  assert.match(unsafeCommandReason("bash -lc \"go test ./...\""), /shell wrapper/);
  assert.match(unsafeCommandReason("pwsh -Command \"npm test\""), /shell wrapper/);
  assert.match(unsafeCommandReason("cmd /c npm test"), /shell wrapper/);
  assert.match(unsafeCommandReason("ssh desktop hostname"), /interactive/);
  assert.match(unsafeCommandReason("tail -f app.log"), /interactive/);
  assert.match(unsafeCommandReason("echo \"unfinished"), /quoting/);
  assert.equal(unsafeCommandReason("go test ./..."), "");
});

test("planTaskWithOpenAI posts to the Responses API and normalizes the result", async () => {
  let request;
  const fetchImpl = async (url, init) => {
    request = { url, init };
    return {
      ok: true,
      status: 200,
      json: async () => ({
        output_text: JSON.stringify({
          ok: true,
          title: "Run smoke test",
          command: "hostname",
          detail: "Prints the selected computer hostname.",
          requiresProject: false,
          outputs: [],
          capability: "commands"
        })
      })
    };
  };

  const result = await planTaskWithOpenAI({
    task: "check this computer",
    projectRoot: "",
    profile: {}
  }, {
    fetchImpl,
    config: {
      configured: true,
      apiKey: "test-key",
      baseURL: "https://api.openai.test/v1",
      model: "test-model"
    },
    timeoutMs: 100
  });

  assert.equal(request.url, "https://api.openai.test/v1/responses");
  assert.equal(request.init.headers.Authorization, "Bearer test-key");
  assert.equal(JSON.parse(request.init.body).model, "test-model");
  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "hostname");
});

test("planTaskWithOpenAI fails closed when it is not configured", async () => {
  const result = await planTaskWithOpenAI({ task: "run something" }, {
    env: {},
    fetchImpl: async () => {
      throw new Error("should not be called");
    }
  });

  assert.equal(result.ok, false);
  assert.match(result.error, /OPENAI_API_KEY/);
});
