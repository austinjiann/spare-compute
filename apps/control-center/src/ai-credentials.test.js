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
    openAIAPIKey: " sk-test ",
    model: " gpt-test "
  }, {
    userDataPath: root,
    safeStorage
  });

  assert.deepEqual(saved, {
    configured: true,
    openAIAPIKey: "sk-test",
    encrypted: true,
    model: "gpt-test",
    source: "app"
  });

  const raw = JSON.parse(await fs.readFile(credentialsPath({ userDataPath: root }), "utf8"));
  assert.equal(raw.openAIAPIKey.encrypted, true);
  assert.notEqual(raw.openAIAPIKey.value, "sk-test");
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
  assert.deepEqual(raw.openAIAPIKey, {
    encrypted: false,
    value: "sk-test"
  });
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
    encrypted: true,
    model: "gpt-app",
    source: "app"
  }, {
    OPENAI_API_KEY: "sk-env",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    source: "app",
    encrypted: true,
    model: "gpt-app"
  });

  assert.deepEqual(credentialsStatus({}, {
    OPENAI_API_KEY: "sk-env",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    source: "environment",
    encrypted: false,
    model: "gpt-env"
  });
});

test("plannerConfigFromCredentials builds the OpenAI planner config", () => {
  assert.deepEqual(plannerConfigFromCredentials({
    openAIAPIKey: "sk-app",
    model: "gpt-app"
  }, {
    OPENAI_API_KEY: "sk-env",
    OPENAI_BASE_URL: "https://api.example.test/v1/",
    OPENAI_MODEL: "gpt-env"
  }), {
    configured: true,
    apiKey: "sk-app",
    baseURL: "https://api.example.test/v1",
    model: "gpt-app"
  });

  assert.equal(plannerConfigFromCredentials({}, {}).configured, false);
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
