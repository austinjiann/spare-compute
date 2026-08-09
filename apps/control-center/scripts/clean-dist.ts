const fs = require("node:fs/promises");
const path = require("node:path");

const controlCenterRoot = path.resolve(__dirname, "..");
const outputDirectory = path.join(controlCenterRoot, "dist");

async function main() {
  if (path.dirname(outputDirectory) !== controlCenterRoot || path.basename(outputDirectory) !== "dist") {
    throw new Error(`Refusing to clean unexpected build directory: ${outputDirectory}`);
  }
  await fs.rm(outputDirectory, { recursive: true, force: true });
}

void main();
