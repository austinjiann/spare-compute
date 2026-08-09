const assert = require("node:assert/strict");
const test = require("node:test");
const {
  outputRestoreDefaultPath,
  shouldOfferOutputRestore
} = require("./output-restore");

test("outputRestoreDefaultPath prefers the job's submitted working directory", () => {
  assert.equal(
    outputRestoreDefaultPath(
      { workingDirectory: " /Users/austin/project-a " },
      { projectRoot: "/Users/austin/project-b" }
    ),
    "/Users/austin/project-a"
  );
});

test("outputRestoreDefaultPath falls back to the selected project", () => {
  assert.equal(
    outputRestoreDefaultPath(
      { workingDirectory: "" },
      { projectRoot: " /Users/austin/project-b " }
    ),
    "/Users/austin/project-b"
  );
});

test("shouldOfferOutputRestore only prompts once for succeeded jobs with outputs", () => {
  const job = {
    id: "job-1",
    canFetchOutputs: true
  };

  assert.equal(shouldOfferOutputRestore({ ok: true, job }), true);
  assert.equal(shouldOfferOutputRestore({ ok: true, job, alreadyOffered: true }), false);
  assert.equal(shouldOfferOutputRestore({ ok: false, job }), false);
  assert.equal(shouldOfferOutputRestore({ ok: true, job: { ...job, canFetchOutputs: false } }), false);
  assert.equal(shouldOfferOutputRestore({ ok: true, job: { canFetchOutputs: true } }), false);
});
