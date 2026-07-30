const assert = require("node:assert/strict");
const test = require("node:test");
const { splitCommandLine } = require("./command-line");

test("splitCommandLine splits simple commands", () => {
  assert.deepEqual(splitCommandLine("go test ./..."), ["go", "test", "./..."]);
});

test("splitCommandLine preserves quoted arguments", () => {
  assert.deepEqual(splitCommandLine('printf "hello world"'), ["printf", "hello world"]);
  assert.deepEqual(splitCommandLine("sh -c 'echo hello'"), ["sh", "-c", "echo hello"]);
});

test("splitCommandLine handles escaped whitespace", () => {
  assert.deepEqual(splitCommandLine("printf hello\\ world"), ["printf", "hello world"]);
});

test("splitCommandLine rejects unfinished quoting", () => {
  assert.throws(() => splitCommandLine('printf "hello'), /unfinished quote/);
});

test("splitCommandLine rejects unfinished escaping", () => {
  assert.throws(() => splitCommandLine("printf hello\\"), /unfinished escape/);
});

test("splitCommandLine returns no tokens for blank input", () => {
  assert.deepEqual(splitCommandLine("  \n\t  "), []);
});
