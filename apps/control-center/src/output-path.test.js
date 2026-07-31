const assert = require("node:assert/strict");
const test = require("node:test");
const {
  normalizeOutputs,
  portableOutputPathError,
  validatePortableOutputs
} = require("./output-path");

test("normalizeOutputs trims empty output declarations", () => {
  assert.deepEqual(normalizeOutputs([" dist ", "", null, " report.pdf "]), [
    "dist",
    "report.pdf"
  ]);
});

test("portableOutputPathError mirrors daemon-safe portable output rules", () => {
  assert.equal(portableOutputPathError("dist/macos/ComputeHop.app"), "");
  assert.match(portableOutputPathError("/tmp/out"), /relative/);
  assert.match(portableOutputPathError("../secret"), /relative/);
  assert.match(portableOutputPathError("dist/../secret"), /parent directories/);
  assert.match(portableOutputPathError("dist\\\\app"), /unsafe/);
  assert.match(portableOutputPathError("report?.pdf"), /unsafe/);
  assert.match(portableOutputPathError("dist/.git/index"), /reserved/);
  assert.match(portableOutputPathError("dist/CON"), /Windows-reserved/);
  assert.match(portableOutputPathError("dist/trailing. "), /dot or space/);
});

test("validatePortableOutputs rejects collisions and too many outputs", () => {
  assert.deepEqual(validatePortableOutputs(["Dist", "report.pdf"]), {
    ok: true,
    outputs: ["Dist", "report.pdf"],
    error: ""
  });
  assert.match(validatePortableOutputs(["Dist", "dist"]).error, /collide/);
  assert.match(validatePortableOutputs(Array.from({ length: 65 }, (_, index) => `out-${index}`)).error, /at most 64/);
});
