import { spawnSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const buildInfoPackage = "venkatasudha.com/codex-folio/internal/buildinfo";
const supportedBuildClasses = new Set(["development", "prerelease", "stable"]);
const supportedDirtyStates = new Set(["clean", "dirty", "unknown"]);

try {
  const buildClass = readBuildClass(process.argv.slice(2));
  const revision = readGitValue(["rev-parse", "--verify", "HEAD"]) ?? "unknown";
  const dirty = revision === "unknown" ? "unknown" : readDirtyState();
  const outputDirectory = join(rootDirectory, "build", "bin");
  const outputName = process.platform === "win32" ? "codex-folio.exe" : "codex-folio";
  const outputPath = join(outputDirectory, outputName);

  if (buildClass === "stable" && (revision === "unknown" || dirty !== "clean")) {
    throw new Error("stable builds require a known source revision and a clean working tree");
  }

  mkdirSync(outputDirectory, { recursive: true });
  run(
    "go",
    [
      "build",
      "-trimpath",
      "-buildvcs=false",
      "-ldflags",
      [
        `-X ${buildInfoPackage}.Revision=${revision}`,
        `-X ${buildInfoPackage}.BuildClass=${buildClass}`,
        `-X ${buildInfoPackage}.Dirty=${dirty}`,
      ].join(" "),
      "-o",
      outputPath,
      "./cmd/codex-folio",
    ],
    rootDirectory,
  );

  console.log(`built ${outputPath}`);
  console.log(`version: ${readVersion()}`);
  console.log(`source revision: ${revision}`);
  console.log(`build classification: ${buildClass}`);
  console.log(`working tree: ${dirty}`);
} catch (error) {
  console.error(`build failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
}

function readBuildClass(args) {
  let buildClass = "development";
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] !== "--build-class") {
      throw new Error(
        `unexpected argument ${JSON.stringify(args[index])}; use --build-class <class>`,
      );
    }
    buildClass = args[index + 1] ?? "";
    index += 1;
  }
  if (!supportedBuildClasses.has(buildClass)) {
    throw new Error(
      `unsupported build class ${JSON.stringify(buildClass)}; choose development, prerelease, or stable`,
    );
  }
  return buildClass;
}

function readVersion() {
  const version = readFileSync(
    resolve(rootDirectory, "internal/buildinfo/version.txt"),
    "utf8",
  ).trim();
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error(`invalid product version ${JSON.stringify(version)}`);
  }
  return version;
}

function readDirtyState() {
  const status = readGitValue(["status", "--porcelain", "--untracked-files=all"]);
  if (status === null) {
    return "unknown";
  }
  const dirty = status.length === 0 ? "clean" : "dirty";
  if (!supportedDirtyStates.has(dirty)) {
    throw new Error(`unsupported dirty state ${JSON.stringify(dirty)}`);
  }
  return dirty;
}

function readGitValue(args) {
  const result = spawnSync("git", args, {
    cwd: rootDirectory,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  });
  if (result.error || result.status !== 0) {
    return null;
  }
  return result.stdout.trim();
}

function run(command, args, cwd) {
  const result = spawnSync(command, args, {
    cwd,
    env: taskEnvironment(),
    stdio: "inherit",
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`${command} exited with status ${result.status ?? "unknown"}`);
  }
}

function taskEnvironment() {
  return {
    ...process.env,
    GOCACHE: process.env.GOCACHE ?? join(tmpdir(), "codex-folio-go-cache"),
  };
}
