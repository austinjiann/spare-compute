const assert = require("node:assert/strict");
const test = require("node:test");
const { loadDevelopmentEnvironment } = require("./development-environment");

test("loadDevelopmentEnvironment loads the app-local env file in development", () => {
  let loadedPath = "";
  const loaded = loadDevelopmentEnvironment({
    envPath: "/tmp/control-center/.env",
    loadEnvFile: (envPath) => {
      loadedPath = envPath;
    }
  });

  assert.equal(loaded, true);
  assert.equal(loadedPath, "/tmp/control-center/.env");
});

test("loadDevelopmentEnvironment skips packaged apps", () => {
  const loaded = loadDevelopmentEnvironment({
    isPackaged: true,
    loadEnvFile: () => {
      throw new Error("should not load");
    }
  });

  assert.equal(loaded, false);
});

test("loadDevelopmentEnvironment tolerates a missing local env file", () => {
  const missing: any = new Error("missing");
  missing.code = "ENOENT";

  const loaded = loadDevelopmentEnvironment({
    envPath: "/tmp/control-center/.env",
    loadEnvFile: () => {
      throw missing;
    }
  });

  assert.equal(loaded, false);
});
