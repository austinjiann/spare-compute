const fs = require("node:fs/promises");
const path = require("node:path");

const credentialFileName = "control-center-ai-credentials.json";

async function loadAIPlannerCredentials(options = {}) {
  try {
    const stored = JSON.parse(await fs.readFile(credentialsPath(options), "utf8"));
    const apiKey = decryptSecret(stored.openAIAPIKey, options.safeStorage);
    return {
      configured: Boolean(apiKey),
      openAIAPIKey: apiKey,
      encrypted: Boolean(stored.openAIAPIKey?.encrypted),
      model: cleanString(stored.model),
      source: "app"
    };
  } catch (error) {
    if (error?.code === "ENOENT" || error instanceof SyntaxError) {
      return emptyCredentials();
    }
    throw error;
  }
}

async function saveAIPlannerCredentials(credentials = {}, options = {}) {
  let apiKey = cleanString(credentials.openAIAPIKey);
  const model = cleanString(credentials.model);
  const filePath = credentialsPath(options);

  if (!apiKey && options.preserveExistingAPIKey) {
    const existing = await loadAIPlannerCredentials(options);
    apiKey = existing.openAIAPIKey;
  }

  if (!apiKey && !model) {
    await clearAIPlannerCredentials(options);
    return emptyCredentials();
  }

  const payload = {
    version: 1,
    model
  };
  if (apiKey) {
    payload.openAIAPIKey = encryptSecret(apiKey, options.safeStorage);
  }

  await fs.mkdir(path.dirname(filePath), { recursive: true, mode: 0o700 });
  const temporaryPath = `${filePath}.${process.pid}.${Date.now()}.tmp`;
  await fs.writeFile(temporaryPath, `${JSON.stringify(payload, null, 2)}\n`, { mode: 0o600 });
  await fs.rename(temporaryPath, filePath);
  return loadAIPlannerCredentials(options);
}

async function clearAIPlannerCredentials(options = {}) {
  try {
    await fs.rm(credentialsPath(options), { force: true });
  } catch (error) {
    if (error?.code !== "ENOENT") {
      throw error;
    }
  }
}

function credentialsStatus(credentials = {}, env = process.env) {
  const model = configuredModel(credentials, env);
  if (credentials.configured) {
    return {
      configured: true,
      source: credentials.source || "app",
      encrypted: Boolean(credentials.encrypted),
      model
    };
  }
  if (cleanString(env.OPENAI_API_KEY)) {
    return {
      configured: true,
      source: "environment",
      encrypted: false,
      model
    };
  }
  return {
    configured: false,
    source: "",
    encrypted: false,
    model
  };
}

function plannerConfigFromCredentials(credentials = {}, env = process.env) {
  const appKey = cleanString(credentials.openAIAPIKey);
  const envKey = cleanString(env.OPENAI_API_KEY);
  return {
    configured: Boolean(appKey || envKey),
    apiKey: appKey || envKey,
    baseURL: cleanBaseURL(env.OPENAI_BASE_URL || "https://api.openai.com/v1"),
    model: configuredModel(credentials, env) || "gpt-5.6"
  };
}

function configuredModel(credentials = {}, env = process.env) {
  return cleanString(credentials.model || env.COMPUTEHOP_OPENAI_MODEL || env.OPENAI_MODEL);
}

function encryptSecret(value, safeStorage) {
  const text = cleanString(value);
  if (canEncrypt(safeStorage)) {
    return {
      encrypted: true,
      value: safeStorage.encryptString(text).toString("base64")
    };
  }
  return {
    encrypted: false,
    value: text
  };
}

function decryptSecret(record, safeStorage) {
  if (!record || typeof record !== "object") {
    return "";
  }
  const value = cleanString(record.value);
  if (!value) {
    return "";
  }
  if (!record.encrypted) {
    return value;
  }
  if (!safeStorage || typeof safeStorage.decryptString !== "function") {
    return "";
  }
  try {
    return safeStorage.decryptString(Buffer.from(value, "base64"));
  } catch {
    return "";
  }
}

function canEncrypt(safeStorage) {
  if (!safeStorage || typeof safeStorage.encryptString !== "function") {
    return false;
  }
  if (typeof safeStorage.isEncryptionAvailable !== "function") {
    return true;
  }
  return safeStorage.isEncryptionAvailable();
}

function credentialsPath(options = {}) {
  const userDataPath = options.userDataPath || options.app?.getPath?.("userData");
  if (!userDataPath) {
    throw new Error("userDataPath is required for Control Center credentials");
  }
  return path.join(userDataPath, credentialFileName);
}

function emptyCredentials() {
  return {
    configured: false,
    openAIAPIKey: "",
    encrypted: false,
    model: "",
    source: ""
  };
}

function cleanBaseURL(value) {
  return (cleanString(value) || "https://api.openai.com/v1").replace(/\/+$/, "");
}

function cleanString(value) {
  return String(value || "").trim();
}

module.exports = {
  clearAIPlannerCredentials,
  credentialsPath,
  credentialsStatus,
  decryptSecret,
  encryptSecret,
  loadAIPlannerCredentials,
  plannerConfigFromCredentials,
  saveAIPlannerCredentials
};
