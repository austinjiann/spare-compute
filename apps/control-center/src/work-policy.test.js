const assert = require("node:assert/strict");
const test = require("node:test");
const {
  capabilityForCommand,
  disallowedWorkMessage,
  filterAllowedSuggestions,
  isWorkAllowed
} = require("./work-policy");

test("capabilityForCommand classifies common work commands", () => {
  assert.equal(capabilityForCommand("go test ./..."), "tests");
  assert.equal(capabilityForCommand("make pr-check"), "tests");
  assert.equal(capabilityForCommand("npm run lint"), "tests");
  assert.equal(capabilityForCommand("go build ./..."), "builds");
  assert.equal(capabilityForCommand("pnpm run build"), "builds");
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
});
