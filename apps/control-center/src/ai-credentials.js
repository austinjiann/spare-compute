const fs = require("node:fs/promises");
const path = require("node:path");

const credentialFileName = "control-center-ai-credentials.json";
const defaultPlannerProvider = "openai";
const defaultPlannerBaseURL = "https://api.openai.com/v1";
const defaultPlannerModel = "gpt-5.6";

async function loadAIPlannerCredentials(options = {}) {
  try {
    const stored = JSON.parse(await fs.readFile(credentialsPath(options), "utf8"));
    const apiKeyRecord = stored.apiKey || stored.openAIAPIKey;
    const apiKey = decryptSecret(apiKeyRecord, options.safeStorage);
    return {
      configured: Boolean(apiKey),
      provider: plannerProvider(stored.provider),
      apiKey,
      openAIAPIKey: apiKey,
      encrypted: Boolean(apiKeyRecord?.encrypted),
      baseURL: cleanOptionalBaseURL(stored.baseURL),
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
  const provider = plannerProvider(credentials.provider);
  let apiKey = cleanString(credentials.apiKey || credentials.openAIAPIKey);
  let baseURL = cleanOptionalBaseURL(credentials.baseURL);
  const model = cleanString(credentials.model);
  const filePath = credentialsPath(options);

  if ((!apiKey || !baseURL) && options.preserveExistingAPIKey) {
    const existing = await loadAIPlannerCredentials(options);
    if (!apiKey) {
      apiKey = existing.apiKey;
    }
    if (!baseURL) {
      baseURL = existing.baseURL;
    }
  }

  if (!apiKey && !model && !baseURL) {
    await clearAIPlannerCredentials(options);
    return emptyCredentials();
  }

  const payload = {
    version: 2,
    provider,
    baseURL,
    model
  };
  if (apiKey) {
    payload.apiKey = encryptSecret(apiKey, options.safeStorage);
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
  const provider = configuredProvider(credentials, env);
  const baseURL = statusBaseURL(credentials, env);
  if (credentials.configured) {
    return {
      configured: true,
      provider,
      source: credentials.source || "app",
      encrypted: Boolean(credentials.encrypted),
      baseURL,
      model
    };
  }
  if (cleanString(env.COMPUTEHOP_AI_API_KEY || env.OPENAI_API_KEY)) {
    return {
      configured: true,
      provider,
      source: "environment",
      encrypted: false,
      baseURL,
      model
    };
  }
  return {
    configured: false,
    provider,
    source: "",
    encrypted: false,
    baseURL,
    model
  };
}

function plannerConfigFromCredentials(credentials = {}, env = process.env) {
  const appKey = cleanString(credentials.apiKey || credentials.openAIAPIKey);
  const envKey = cleanString(env.COMPUTEHOP_AI_API_KEY || env.OPENAI_API_KEY);
  return {
    configured: Boolean(appKey || envKey),
    provider: configuredProvider(credentials, env),
    apiKey: appKey || envKey,
    baseURL: configuredBaseURL(credentials, env),
    model: configuredModel(credentials, env) || defaultPlannerModel
  };
}

function configuredModel(credentials = {}, env = process.env) {
  return cleanString(
    credentials.model ||
    env.COMPUTEHOP_AI_MODEL ||
    env.COMPUTEHOP_OPENAI_MODEL ||
    env.OPENAI_MODEL
  );
}

function configuredProvider(credentials = {}, env = process.env) {
  return plannerProvider(credentials.provider || env.COMPUTEHOP_AI_PROVIDER);
}

function configuredBaseURL(credentials = {}, env = process.env) {
  return cleanBaseURL(
    credentials.baseURL ||
    env.COMPUTEHOP_AI_BASE_URL ||
    env.OPENAI_BASE_URL ||
    defaultPlannerBaseURL
  );
}

function statusBaseURL(credentials = {}, env = process.env) {
  return cleanOptionalBaseURL(
    credentials.baseURL ||
    env.COMPUTEHOP_AI_BASE_URL ||
    env.OPENAI_BASE_URL
  );
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
    provider: defaultPlannerProvider,
    apiKey: "",
    openAIAPIKey: "",
    encrypted: false,
    baseURL: "",
    model: "",
    source: ""
  };
}

function cleanBaseURL(value) {
  return (cleanString(value) || defaultPlannerBaseURL).replace(/\/+$/, "");
}

function cleanOptionalBaseURL(value) {
  const text = cleanString(value);
  return text ? cleanBaseURL(text) : "";
}

function plannerProvider(value) {
  const provider = cleanString(value).toLowerCase();
  if (provider === "openai-compatible") {
    return defaultPlannerProvider;
  }
  return provider === defaultPlannerProvider ? provider : defaultPlannerProvider;
}

function cleanString(value) {
  return String(value || "").trim();
}

module.exports = {
  clearAIPlannerCredentials,
  credentialsPath,
  credentialsStatus,
  decryptSecret,
  defaultPlannerBaseURL,
  defaultPlannerModel,
  defaultPlannerProvider,
  encryptSecret,
  loadAIPlannerCredentials,
  plannerConfigFromCredentials,
  saveAIPlannerCredentials
};
