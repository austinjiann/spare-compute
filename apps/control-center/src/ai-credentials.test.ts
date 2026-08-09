const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const {
  clearAIPlannerCredentials,
  credentialsPath,
  credentialsStatus,
  decryptSecret,
  encryptSecret,
  loadAIPlannerCredentials,
  plannerConfigFromCredentials,
  saveAIPlannerCredentials
} = require("./ai-credentials");

test("saveAIPlannerCredentials encrypts API keys when safeStorage is available", async (t) => {
  const root = await tempRoot(t);
  const safeStorage = fakeSafeStorage();

  const saved = await saveAIPlannerCredentials({
    apiKey: " sk-test ",
    baseURL: " https://api.openai.test/v1/ ",
    model: " gpt-test "
  }, {
    userDataPath: root,
    safeStorage
  });

  assert.deepEqual(saved, {
    configured: true,
    provider: "openai",
    apiKey: "sk-test",
    openAIAPIKey: "sk-test",
    encrypted: true,
    baseURL: "https://api.openai.test/v1",
    model: "gpt-test",
    source: "app"
  });

  const raw = JSON.parse(await fs.readFile(credentialsPath({ userDataPath: root }), "utf8"));
  assert.equal(raw.version, 2);
  assert.equal(raw.provider, "openai");
  assert.equal(raw.baseURL, "https://api.openai.test/v1");
  assert.equal(raw.apiKey.encrypted, true);
  assert.notEqual(raw.apiKey.value, "sk-test");
  assert.equal(raw.openAIAPIKey, undefined);
});

test("saveAIPlannerCredentials falls back to plaintext only when encryption is unavailable", async (t) => {
  const root = await tempRoot(t);
  await saveAIPlannerCredentials({
    openAIAPIKey: "sk-test"
  }, {
    userDataPath: root,
    safeStorage: {
      isEncryptionAvailable: () => false
    }
  });

  const raw = JSON.parse(await fs.readFile(credentialsPath({ userDataPath: root }), "utf8"));
  assert.deepEqual(raw.apiKey, {
    encrypted: false,
    value: "sk-test"
  });
});

test("loadAIPlannerCredentials reads legacy OpenAI key files", async (t) => {
  const root = await tempRoot(t);
  await fs.writeFile(credentialsPath({ userDataPath: root }), `${JSON.stringify({
    version: 1,
    openAIAPIKey: {
      encrypted: false,
      value: "sk-legacy"
    },
    model: "gpt-legacy"
  })}\n`);

  const loaded = await loadAIPlannerCredentials({ userDataPath: root });

  assert.equal(loaded.configured, true);
  assert.equal(loaded.provider, "openai");
  assert.equal(loaded.apiKey, "sk-legacy");
  assert.equal(loaded.openAIAPIKey, "sk-legacy");
  assert.equal(loaded.model, "gpt-legacy");
});

test("saveAIPlannerCredentials can preserve an existing key while updating model", async (t) => {
  const root = await tempRoot(t);
  const safeStorage = fakeSafeStorage();
  await saveAIPlannerCredentials({
    openAIAPIKey: "sk-test",
    model: "gpt-old"
  }, {
    userDataPath: root,
    safeStorage
  });

  const saved = await saveAIPlannerCredentials({
    openAIAPIKey: "",
    model: "gpt-new"
  }, {
    userDataPath: root,
    safeStorage,
    preserveExistingAPIKey: true
  });

  assert.equal(saved.configured, true);
  assert.equal(saved.openAIAPIKey, "sk-test");
  assert.equal(saved.model, "gpt-new");
});

test("loadAIPlannerCredentials fails closed when encrypted data cannot be decrypted", async (t) => {
  const root = await tempRoot(t);
  await saveAIPlannerCredentials({
    openAIAPIKey: "sk-test"
  }, {
    userDataPath: root,
    safeStorage: fakeSafeStorage()
  });

  const loaded = await loadAIPlannerCredentials({ userDataPath: root });

  assert.equal(loaded.configured, false);
  assert.equal(loaded.openAIAPIKey, "");
});

test("clearAIPlannerCredentials removes stored credentials", async (t) => {
  const root = await tempRoot(t);
  await saveAIPlannerCredentials({
    openAIAPIKey: "sk-test",
    model: "gpt-test"
  }, {
    userDataPath: root
  });

  await clearAIPlannerCredentials({ userDataPath: root });
  const loaded = await loadAIPlannerCredentials({ userDataPath: root });

  assert.equal(loaded.configured, false);
});

test("credentialsStatus prefers app credentials and falls back to environment", () => {
  assert.deepEqual(credentialsStatus({
    configured: true,
    provider: "openai",
    encrypted: true,
    baseURL: "https://api.openai.test/v1",
    model: "gpt-app",
    source: "app"
  }, {
    OPENAI_API_KEY: "sk-env",
    OPENAI_BASE_URL: "https://api.env.test/v1",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    provider: "openai",
    source: "app",
    encrypted: true,
    baseURL: "https://api.openai.test/v1",
    model: "gpt-app"
  });

  assert.deepEqual(credentialsStatus({}, {
    OPENAI_API_KEY: "sk-env",
    COMPUTEHOP_AI_BASE_URL: "https://api.env.test/v1/",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    provider: "openai",
    source: "environment",
    encrypted: false,
    baseURL: "https://api.env.test/v1",
    model: "gpt-env"
  });
});

test("credentialsStatus reports the same model precedence used by planner config", () => {
  assert.deepEqual(credentialsStatus({
    configured: false,
    model: "gpt-app"
  }, {
    OPENAI_API_KEY: "sk-env",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    provider: "openai",
    source: "environment",
    encrypted: false,
    baseURL: "",
    model: "gpt-app"
  });

  assert.deepEqual(credentialsStatus({
    configured: true,
    encrypted: true,
    model: "",
    source: "app"
  }, {
    COMPUTEHOP_OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    provider: "openai",
    source: "app",
    encrypted: true,
    baseURL: "",
    model: "gpt-env"
  });
});

test("plannerConfigFromCredentials builds the planner config from app and generic env fields", () => {
  assert.deepEqual(plannerConfigFromCredentials({
    provider: "openai",
    apiKey: "sk-app",
    baseURL: "https://api.app.test/v1/",
    model: "gpt-app"
  }, {
    OPENAI_API_KEY: "sk-env",
    COMPUTEHOP_AI_API_KEY: "sk-generic-env",
    OPENAI_BASE_URL: "https://api.example.test/v1/",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    provider: "openai",
    apiKey: "sk-app",
    baseURL: "https://api.app.test/v1",
    model: "gpt-app"
  });

  assert.deepEqual(plannerConfigFromCredentials({}, {
    COMPUTEHOP_AI_API_KEY: "sk-generic-env",
    COMPUTEHOP_AI_BASE_URL: "https://api.generic.test/v1/",
    COMPUTEHOP_AI_MODEL: "gpt-generic"
  }), {
    configured: true,
    provider: "openai",
    apiKey: "sk-generic-env",
    baseURL: "https://api.generic.test/v1",
    model: "gpt-generic"
  });

  assert.deepEqual(plannerConfigFromCredentials({}, {}), {
    configured: false,
    provider: "openai",
    apiKey: "",
    baseURL: "https://api.openai.com/v1",
    model: "gpt-5.6-luna"
  });
});

test("encryptSecret and decryptSecret round-trip through safeStorage", () => {
  const safeStorage = fakeSafeStorage();
  const encrypted = encryptSecret("sk-test", safeStorage);

  assert.equal(encrypted.encrypted, true);
  assert.equal(decryptSecret(encrypted, safeStorage), "sk-test");
});

function fakeSafeStorage() {
  return {
    isEncryptionAvailable: () => true,
    encryptString: (value) => Buffer.from(`encrypted:${value}`),
    decryptString: (buffer) => {
      const value = buffer.toString("utf8");
      return value.startsWith("encrypted:") ? value.slice("encrypted:".length) : "";
    }
  };
}

async function tempRoot(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "computehop-ai-credentials-"));
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
  });
  return root;
}
