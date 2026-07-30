const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  planControlCenterTask,
  shouldTryAIPlanner
} = require("./planner-service");

test("planControlCenterTask uses deterministic local planning before AI", async (t) => {
  const project = await tempProject(t, {
    "go.mod": "module example.com/app\n"
  });
  let calls = 0;

  const result = await planControlCenterTask({
    task: "run tests",
    projectRoot: project
  }, {
    config: {
      configured: true,
      apiKey: "key",
      baseURL: "https://api.openai.test/v1",
      model: "test-model"
    },
    fetchImpl: async () => {
      calls += 1;
      throw new Error("should not call AI");
    }
  });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "go test ./...");
  assert.equal(calls, 0);
});

test("planControlCenterTask falls back to AI for unknown local tasks", async () => {
  const result = await planControlCenterTask({
    task: "please tell me which computer this is",
    projectRoot: ""
  }, {
    config: {
      configured: true,
      apiKey: "key",
      baseURL: "https://api.openai.test/v1",
      model: "test-model"
    },
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        output_text: JSON.stringify({
          ok: true,
          title: "Show hostname",
          command: "hostname",
          detail: "Prints the worker hostname.",
          requiresProject: false,
          capability: "commands"
        })
      })
    }),
    timeoutMs: 100
  });

  assert.equal(result.ok, true);
  assert.equal(result.plan.command, "hostname");
  assert.equal(result.plan.planner, "openai");
});

test("planControlCenterTask does not let AI bypass missing-project guidance", async () => {
  let calls = 0;
  const result = await planControlCenterTask({
    task: "run tests",
    projectRoot: ""
  }, {
    config: {
      configured: true,
      apiKey: "key",
      baseURL: "https://api.openai.test/v1",
      model: "test-model"
    },
    fetchImpl: async () => {
      calls += 1;
      return {
        ok: true,
        status: 200,
        json: async () => ({ output_text: "{}" })
      };
    }
  });

  assert.equal(result.ok, false);
  assert.match(result.error, /Choose a project first/);
  assert.equal(calls, 0);
});

test("planControlCenterTask keeps the local error when AI fails", async () => {
  const result = await planControlCenterTask({
    task: "do the special release thing",
    projectRoot: ""
  }, {
    config: {
      configured: true,
      apiKey: "key",
      baseURL: "https://api.openai.test/v1",
      model: "test-model"
    },
    fetchImpl: async () => ({
      ok: false,
      status: 500,
      json: async () => ({})
    })
  });

  assert.equal(result.ok, false);
  assert.match(result.error, /could not turn that into a safe local command/i);
  assert.equal(result.aiPlanner.attempted, true);
  assert.match(result.aiPlanner.error, /HTTP 500/);
});

test("shouldTryAIPlanner requires configuration and skips missing-project errors", () => {
  assert.equal(shouldTryAIPlanner(
    { ok: false, error: "I could not turn that into a safe local command yet." },
    { task: "custom task" },
    { env: {} }
  ), false);
  assert.equal(shouldTryAIPlanner(
    { ok: false, error: "Choose a project first so ComputeHop can pick the right command." },
    { task: "run tests" },
    {
      config: {
        configured: true,
        apiKey: "key",
        baseURL: "https://api.openai.test/v1",
        model: "test-model"
      }
    }
  ), false);
});

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
