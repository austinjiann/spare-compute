const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { planControlCenterTask } = require("./planner-service");

test("planControlCenterTask uses AI for common requests with grounded project metadata", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n",
    Makefile: "pr-check:\n\tgo test ./...\n"
  });
  let body;

  const result = await planControlCenterTask({
    task: "run project checks",
    projectRoot: project
  }, plannerOptions(async (_url, init) => {
    body = JSON.parse(init.body);
    return responsePlan({
      title: "Run project checks",
      command: "make pr-check",
      detail: "Runs the project's checks.",
      requiresProject: true,
      capability: "tests"
    });
  }));

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "make pr-check");
  assert.equal(result.plan.planner, "openai");
  assert.deepEqual(result.plan.requiredToolIDs, ["go", "make"]);
  const prompt = JSON.parse(body.input[1].content);
  assert.deepEqual(prompt.project.makeTargets, ["pr-check"]);
  assert.equal(prompt.project.files["go.mod"], true);
});

test("planControlCenterTask preserves worker placement through AI planning", async () => {
  const result = await planControlCenterTask({
    task: "show the hostname on the worker",
    projectRoot: ""
  }, plannerOptions(async () => responsePlan({
    title: "Show hostname",
    command: "hostname",
    detail: "Shows the worker's hostname.",
    requiresProject: false,
    capability: "commands"
  })));

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "hostname");
  assert.equal(result.plan.targetPreference, "worker");
});

test("planControlCenterTask turns model project requirements into a folder action", async () => {
  const result = await planControlCenterTask({
    task: "run tests",
    projectRoot: ""
  }, plannerOptions(async () => responsePlan({
    ok: false,
    title: "Project needed",
    command: "",
    detail: "Choose the project to test.",
    requiresProject: true,
    capability: "tests"
  })));

  assert.equal(result.ok, false);
  assert.match(result.error, /Choose a project first/);
  assert.equal(result.actionKind, "choose-project");
  assert.equal(result.actionLabel, "Choose project");
});

test("planControlCenterTask requires configured AI planning", async () => {
  const result = await planControlCenterTask({ task: "run tests" }, {
    env: {},
    fetchImpl: async () => {
      throw new Error("should not call");
    }
  });

  assert.equal(result.ok, false);
  assert.match(result.error, /OPENAI_API_KEY/);
});

test("planControlCenterTask rejects empty requests before calling AI", async () => {
  let calls = 0;
  const result = await planControlCenterTask({ task: "  " }, plannerOptions(async () => {
    calls += 1;
    throw new Error("should not call");
  }));

  assert.equal(result.ok, false);
  assert.match(result.error, /Enter what you want/);
  assert.equal(calls, 0);
});

test("planControlCenterTask reports planner request failures directly", async () => {
  const result = await planControlCenterTask({ task: "do the thing" }, plannerOptions(async () => ({
    ok: false,
    status: 500,
    json: async () => ({})
  })));

  assert.equal(result.ok, false);
  assert.match(result.error, /HTTP 500/);
});

function plannerOptions(fetchImpl) {
  return {
    config: {
      configured: true,
      apiKey: "key",
      baseURL: "https://api.openai.test/v1",
      model: "gpt-5.6-luna"
    },
    fetchImpl,
    timeoutMs: 100
  };
}

function responsePlan(plan) {
  return {
    ok: true,
    status: 200,
    json: async () => ({
      output_text: JSON.stringify({
        ok: plan.ok ?? true,
        title: plan.title,
        command: plan.command,
        detail: plan.detail,
        requiresProject: plan.requiresProject,
        outputs: plan.outputs || [],
        capability: plan.capability
      })
    })
  };
}

async function tempProject(t, files) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-planner-service-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  for (const [name, contents] of Object.entries(files)) {
    await fs.writeFile(path.join(root, name), contents);
  }
  return root;
}
