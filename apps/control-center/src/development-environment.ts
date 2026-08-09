const path = require("node:path");
const { controlCenterRootForModule } = require("./runtime-paths");

function loadDevelopmentEnvironment(options: any = {}) {
  if (options.isPackaged) {
    return false;
  }

  const loadEnvFile = options.loadEnvFile || process.loadEnvFile;
  if (typeof loadEnvFile !== "function") {
    return false;
  }

  const envPath = options.envPath || path.join(
    controlCenterRootForModule(options.moduleDirectory || __dirname),
    ".env"
  );
  try {
    loadEnvFile(envPath);
    return true;
  } catch (error) {
    if (error?.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

module.exports = {
  loadDevelopmentEnvironment
};
