const assert = require("node:assert/strict");
const test = require("node:test");
const { friendlyJobFailure } = require("./job-failure");

test("friendlyJobFailure explains missing worker tools", () => {
  assert.equal(
    friendlyJobFailure(
      {
        executable: "go",
        command: "go test ./...",
        failure: 'start native process: start "go": exec: "go": executable file not found in $PATH'
      },
      { targetName: "Gaming PC" }
    ),
    "Go is not installed on Gaming PC, or it is not on PATH. Install it there or choose a different computer."
  );
});

test("friendlyJobFailure explains missing project folders", () => {
  assert.equal(
    friendlyJobFailure(
      {
        executable: "npm",
        command: "npm test",
        failure: "start native process: chdir /tmp/computehop/job: no such file or directory"
      },
      { targetName: "Mini PC" }
    ),
    "The project folder was not available on Mini PC. Choose the project again and retry."
  );
});

test("friendlyJobFailure explains nonzero exits without hiding logs", () => {
  assert.equal(
    friendlyJobFailure(
      {
        executable: "make",
        command: "make pr-check",
        failure: "process exited with code 2"
      },
      { targetName: "this Mac" }
    ),
    "make pr-check exited with code 2 on this Mac. Open logs for details."
  );
});

test("friendlyJobFailure preserves unknown failures", () => {
  assert.equal(
    friendlyJobFailure({ failure: "runner was stopped: context canceled" }),
    "runner was stopped: context canceled"
  );
});
