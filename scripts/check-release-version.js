#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");

const repositoryRoot = path.resolve(__dirname, "..");

function readText(relativePath) {
  return fs.readFileSync(path.join(repositoryRoot, relativePath), "utf8");
}

function readJSON(relativePath) {
  return JSON.parse(readText(relativePath));
}

function plistString(plist, key) {
  const pattern = new RegExp(`<key>${escapeRegExp(key)}</key>\\s*<string>([^<]+)</string>`);
  const match = plist.match(pattern);
  return match ? match[1].trim() : "";
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function assertEqual(errors, label, actual, expected) {
  if (actual !== expected) {
    errors.push(`${label} is ${JSON.stringify(actual)}, expected ${JSON.stringify(expected)}`);
  }
}

function main() {
  const errors = [];
  const version = readText("VERSION").trim();
  if (!/^\d+\.\d+\.\d+$/.test(version)) {
    errors.push(`VERSION must be semantic x.y.z, got ${JSON.stringify(version)}`);
  }

  const controlCenterPackage = readJSON("apps/control-center/package.json");
  const controlCenterLock = readJSON("apps/control-center/package-lock.json");
  const infoPlist = readText("packaging/macos/Info.plist");

  assertEqual(errors, "apps/control-center/package.json version", controlCenterPackage.version, version);
  assertEqual(errors, "apps/control-center/package-lock.json version", controlCenterLock.version, version);
  assertEqual(
    errors,
    "apps/control-center/package-lock.json root package version",
    controlCenterLock.packages && controlCenterLock.packages[""] && controlCenterLock.packages[""].version,
    version
  );
  assertEqual(
    errors,
    "packaging/macos/Info.plist CFBundleShortVersionString",
    plistString(infoPlist, "CFBundleShortVersionString"),
    version
  );

  if (errors.length > 0) {
    for (const error of errors) {
      console.error(error);
    }
    process.exit(1);
  }

  console.log(`Release version metadata is consistent: ${version}`);
}

main();
