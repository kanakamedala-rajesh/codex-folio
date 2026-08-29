import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { assertBuild } from "./smoke.mjs";

const webDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distDirectory = join(webDirectory, "dist");

runNpm(["run", "build"]);
assertBuild(distDirectory);
const firstBuild = snapshot(distDirectory);

runNpm(["run", "build"]);
assertBuild(distDirectory);
const secondBuild = snapshot(distDirectory);

assert.deepEqual(secondBuild, firstBuild, "two frontend builds produced different files");
console.log(`frontend test: deterministic self-contained build (${firstBuild.length} files)`);

function runNpm(args) {
  const command = process.platform === "win32" ? "npm.cmd" : "npm";
  const result = spawnSync(command, args, {
    cwd: webDirectory,
    encoding: "utf8",
    stdio: "inherit",
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

function snapshot(directory) {
  return collectFiles(directory)
    .map((filePath) => [relative(directory, filePath), readFileSync(filePath).toString("base64")])
    .sort(([left], [right]) => left.localeCompare(right));
}

function collectFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? collectFiles(path) : [path];
  });
}
