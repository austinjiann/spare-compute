const assert = require("node:assert/strict");
const test = require("node:test");
const { formatCommandLine, splitCommandLine } = require("./command-line");

test("splitCommandLine splits simple commands", () => {
  assert.deepEqual(splitCommandLine("go test ./..."), ["go", "test", "./..."]);
});

test("splitCommandLine preserves quoted arguments", () => {
  assert.deepEqual(splitCommandLine('printf "hello world"'), ["printf", "hello world"]);
  assert.deepEqual(splitCommandLine("sh -c 'echo hello'"), ["sh", "-c", "echo hello"]);
});

test("splitCommandLine preserves empty quoted arguments", () => {
  assert.deepEqual(splitCommandLine('printf ""'), ["printf", ""]);
  assert.deepEqual(splitCommandLine("printf '' done"), ["printf", "", "done"]);
});

test("splitCommandLine joins adjacent quoted and unquoted segments", () => {
  assert.deepEqual(splitCommandLine('printf hello""world'), ["printf", "helloworld"]);
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

test("formatCommandLine preserves simple command display", () => {
  assert.equal(formatCommandLine(["go", "test", "./..."]), "go test ./...");
});

test("formatCommandLine quotes arguments with spaces", () => {
  assert.equal(formatCommandLine(["printf", "hello world"]), 'printf "hello world"');
});

test("formatCommandLine preserves empty arguments", () => {
  assert.equal(formatCommandLine(["printf", ""]), 'printf ""');
});

test("formatCommandLine escapes display-sensitive quoted characters", () => {
  assert.equal(formatCommandLine(["sh", "-c", 'echo "$HOME"']), 'sh -c "echo \\"\\$HOME\\""');
});
