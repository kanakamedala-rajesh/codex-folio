#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { existsSync, rmSync } from "node:fs";
import { dirname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const cleanupPaths = [
  ".impeccable/hook.cache.json",
  "build",
  "web/dist",
  "web/node_modules",
];

const argumentsList = process.argv.slice(2);
if (argumentsList.includes("--help")) {
  console.log("Usage: node scripts/prepare-windows-copy.mjs [Windows destination]");
  console.log(
    "Cleans generated repository artifacts and prints the PowerShell copy and test commands.",
  );
  process.exit(0);
}

if (argumentsList.length > 1) {
  throw new Error("provide at most one Windows destination path");
}

for (const relativePath of cleanupPaths) {
  const absolutePath = resolve(repositoryRoot, relativePath);
  if (!absolutePath.startsWith(`${repositoryRoot}${sep}`)) {
    throw new Error(`refusing to clean a path outside the repository: ${relativePath}`);
  }

  if (existsSync(absolutePath)) {
    rmSync(absolutePath, { force: true, recursive: true });
    console.log(`removed ${relativePath}`);
  }
}

const sourcePath = wslWindowsPath(repositoryRoot);
const destinationPath =
  argumentsList[0] ??
  process.env.CODEX_FOLIO_WINDOWS_DESTINATION ??
  `${detectWindowsUserProfile() ?? "C:\\Users\\<WindowsUser>"}\\src\\codex-folio`;
const excludedPaths = [".git", "build", "web\\dist", "web\\node_modules"].map((relativePath) =>
  appendWindowsPath(sourcePath, relativePath),
);

console.log("\nRun this in PowerShell from the WSL project directory:");
console.log(
  [
    "robocopy",
    powershellQuote(sourcePath),
    powershellQuote(destinationPath),
    "/E",
    "/XD",
    ...excludedPaths.map(powershellQuote),
  ].join(" "),
);
console.log("\nThen run this from the copied NTFS-backed directory:");
console.log(`Set-Location ${powershellQuote(destinationPath)}`);
console.log("go test -count=1 .\\internal\\platform .\\internal\\store .\\cmd\\codex-folio");

function wslWindowsPath(path) {
  try {
    return execFileSync("wslpath", ["-w", path], { encoding: "utf8" }).trim();
  } catch {
    throw new Error("wslpath is required; run this helper inside WSL");
  }
}

function detectWindowsUserProfile() {
  const commands = [
    ["powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", "$env:USERPROFILE"]],
    ["cmd.exe", ["/C", "echo %USERPROFILE%"]],
  ];

  for (const [command, commandArguments] of commands) {
    try {
      const output = execFileSync(command, commandArguments, {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      });
      const profile = output
        .split(/\r?\n/)
        .map((line) => line.trim())
        .find((line) => /^[A-Za-z]:[\\/]/.test(line));
      if (profile) {
        return profile.replaceAll("/", "\\");
      }
    } catch {
      // WSL interop can be disabled; the explicit destination argument remains available.
    }
  }

  return null;
}

function appendWindowsPath(base, suffix) {
  return `${base.replace(/[\\/]+$/, "")}\\${suffix.replaceAll("/", "\\")}`;
}

function powershellQuote(value) {
  return `'${value.replaceAll("'", "''")}'`;
}
