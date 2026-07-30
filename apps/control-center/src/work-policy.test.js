const assert = require("node:assert/strict");
const test = require("node:test");
const {
  capabilityForCommand,
  disallowedWorkMessage,
  filterAllowedSuggestions,
  isSafeUtilityCommand,
  isWorkAllowed
} = require("./work-policy");

test("capabilityForCommand classifies common work commands", () => {
  assert.equal(capabilityForCommand("go test ./..."), "tests");
  assert.equal(capabilityForCommand("make pr-check"), "tests");
  assert.equal(capabilityForCommand("npm run lint"), "tests");
  assert.equal(capabilityForCommand("go build ./..."), "builds");
  assert.equal(capabilityForCommand("pnpm run build"), "builds");
  assert.equal(capabilityForCommand("make macos-package"), "builds");
  assert.equal(capabilityForCommand("docker compose build"), "docker");
  assert.equal(capabilityForCommand("docker build ."), "docker");
  assert.equal(capabilityForCommand("ffmpeg -i in.mov out.mp4"), "video");
  assert.equal(capabilityForCommand("ollama run llama3"), "ai");
  assert.equal(capabilityForCommand("hostname"), "");
});

test("filterAllowedSuggestions removes disabled work categories", () => {
  const suggestions = [
    { label: "Check", command: "make pr-check" },
    { label: "Build", command: "npm run build" },
    { label: "Docker", command: "docker build ." },
    { label: "Smoke", command: "hostname" }
  ];

  const filtered = filterAllowedSuggestions(suggestions, {
    tests: false,
    docker: false
  });

  assert.deepEqual(filtered.map((suggestion) => suggestion.label), ["Build", "Smoke"]);
});

test("disallowedWorkMessage explains blocked planned work", () => {
  const plan = { command: "docker compose build" };

  assert.equal(isWorkAllowed(plan, { docker: true }), true);
  assert.equal(isWorkAllowed(plan, { docker: false }), false);
  assert.match(disallowedWorkMessage(plan, { docker: false }), /Docker is turned off/);
  assert.match(disallowedWorkMessage(plan, { docker: false }), /Open Advanced/);
});

test("exact unknown commands require the exact command allowance", () => {
  const exactCommand = { command: "echo hello", exact: true };

  assert.equal(isWorkAllowed(exactCommand, { commands: true }), true);
  assert.equal(isWorkAllowed(exactCommand, { commands: false }), false);
  assert.match(disallowedWorkMessage(exactCommand, { commands: false }), /Exact commands is turned off/);
  assert.match(disallowedWorkMessage(exactCommand, { commands: false }), /Allow on selected device/);
});

test("safe utility commands do not require the exact command allowance", () => {
  const hostname = { command: "hostname", exact: true };
  const uname = { command: "uname -a", exact: true };
  const absoluteWhoami = { command: "/usr/bin/whoami", exact: true };

  assert.equal(isSafeUtilityCommand("hostname"), true);
  assert.equal(isSafeUtilityCommand("/bin/hostname"), true);
  assert.equal(isSafeUtilityCommand("uname -a"), true);
  assert.equal(isSafeUtilityCommand("uname --all"), false);
  assert.equal(isSafeUtilityCommand("hostname && whoami"), false);
  assert.equal(isWorkAllowed(hostname, { commands: false }), true);
  assert.equal(isWorkAllowed(uname, { commands: false }), true);
  assert.equal(isWorkAllowed(absoluteWhoami, { commands: false }), true);
  assert.equal(disallowedWorkMessage(hostname, { commands: false }), "");
});

test("recognized exact commands still use their specific category", () => {
  const dockerCommand = { command: "docker build .", exact: true };

  assert.equal(isWorkAllowed(dockerCommand, { commands: false, docker: true }), true);
  assert.equal(isWorkAllowed(dockerCommand, { commands: true, docker: false }), false);
  assert.match(disallowedWorkMessage(dockerCommand, { docker: false }), /Docker is turned off/);
});
