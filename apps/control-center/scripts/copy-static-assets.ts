const fs = require("node:fs/promises");
const path = require("node:path");

const controlCenterRoot = path.resolve(__dirname, "..");
const sourceDirectory = path.join(controlCenterRoot, "src");
const outputDirectory = path.join(controlCenterRoot, "dist", "src");
const staticAssets = ["index.html", "styles.css"];

async function main() {
  await fs.mkdir(outputDirectory, { recursive: true });
  await Promise.all(staticAssets.map((asset) => (
    fs.copyFile(path.join(sourceDirectory, asset), path.join(outputDirectory, asset))
  )));
}

void main();
