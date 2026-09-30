import { spawnSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const buildInfoPackage = "venkatasudha.com/codex-folio/internal/buildinfo";
const supportedBuildClasses = new Set(["development", "prerelease", "stable"]);
const supportedDirtyStates = new Set(["clean", "dirty", "unknown"]);
const supportedTargets = new Map([
  [
    "linux-amd64",
    { goos: "linux", goarch: "amd64", executable: "codex-folio" },
  ],
  [
    "windows-amd64",
    { goos: "windows", goarch: "amd64", executable: "codex-folio.exe" },
  ],
  [
    "macos-arm64",
    { goos: "darwin", goarch: "arm64", executable: "codex-folio" },
  ],
]);

try {
  const { buildClass, targetName } = readArguments(process.argv.slice(2));
  const target =
    targetName === null ? nativeTarget() : supportedTargets.get(targetName);
  const revision = readGitValue(["rev-parse", "--verify", "HEAD"]) ?? "unknown";
  const dirty = revision === "unknown" ? "unknown" : readDirtyState();
  const outputDirectory =
    targetName === null
      ? join(rootDirectory, "build", "bin")
      : join(rootDirectory, "build", "targets", targetName);
  const outputPath = join(outputDirectory, target.executable);
  const buildMode = isNativeTarget(target) ? "native" : "cross-compiled";

  if (
    buildClass === "stable" &&
    (revision === "unknown" || dirty !== "clean")
  ) {
    throw new Error(
      "stable builds require a known source revision and a clean working tree",
    );
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
    target,
    targetName,
  );

  if (target.goos === "linux") {
    const helperPath = join(outputDirectory, "codex-folio-wsl-vault.exe");
    run(
      "go",
      ["build", "-trimpath", "-buildvcs=false", "-o", helperPath, "./cmd/codex-folio-wsl-vault"],
      rootDirectory,
      { goos: "windows", goarch: "amd64" },
      "windows-amd64",
    );
    console.log(`built ${helperPath}`);
  }

  console.log(`built ${outputPath}`);
  if (targetName !== null) console.log(`target: ${targetName}`);
  console.log(`version: ${readVersion()}`);
  console.log(`source revision: ${revision}`);
  console.log(`build classification: ${buildClass}`);
  console.log(`working tree: ${dirty}`);
  if (targetName !== null) {
    console.log(`build mode: ${buildMode}`);
    console.log("qualification: compile-only; no native runtime qualification");
  }
} catch (error) {
  console.error(
    `build failed: ${error instanceof Error ? error.message : String(error)}`,
  );
  process.exitCode = 1;
}

function readArguments(args) {
  let buildClass = "development";
  let targetName = null;
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === "--build-class") {
      buildClass = args[index + 1] ?? "";
      index += 1;
      continue;
    }
    if (args[index] === "--target") {
      targetName = args[index + 1] ?? "";
      index += 1;
      continue;
    }
    throw new Error(
      `unexpected argument ${JSON.stringify(args[index])}; use --build-class <class> or --target <target>`,
    );
  }
  if (!supportedBuildClasses.has(buildClass)) {
    throw new Error(
      `unsupported build class ${JSON.stringify(buildClass)}; choose development, prerelease, or stable`,
    );
  }
  if (targetName !== null && !supportedTargets.has(targetName)) {
    throw new Error(
      `unsupported target ${JSON.stringify(targetName)}; choose ${[...supportedTargets.keys()].join(", ")}`,
    );
  }
  return { buildClass, targetName };
}

function nativeTarget() {
  return {
    goos: process.platform === "win32" ? "windows" : process.platform,
    goarch: process.arch === "x64" ? "amd64" : process.arch,
    executable:
      process.platform === "win32" ? "codex-folio.exe" : "codex-folio",
  };
}

function isNativeTarget(target) {
  const native = nativeTarget();
  return target.goos === native.goos && target.goarch === native.goarch;
}

function readVersion() {
  const version = readFileSync(
    resolve(rootDirectory, "internal/buildinfo/version.txt"),
    "utf8",
  ).trim();
  if (
    !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)
  ) {
    throw new Error(`invalid product version ${JSON.stringify(version)}`);
  }
  return version;
}

function readDirtyState() {
  const status = readGitValue([
    "status",
    "--porcelain",
    "--untracked-files=all",
  ]);
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

function run(command, args, cwd, target, targetName) {
  const result = spawnSync(command, args, {
    cwd,
    env: taskEnvironment(target, targetName),
    stdio: "inherit",
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    const buildTarget = targetName ?? `${target.goos}-${target.goarch}`;
    throw new Error(
      `${buildTarget} build failed: ${command} exited with status ${result.status ?? "unknown"}`,
    );
  }
}

function taskEnvironment(target, targetName) {
  const environment = {
    ...process.env,
    GOCACHE: process.env.GOCACHE ?? join(tmpdir(), "codex-folio-go-cache"),
  };
  if (targetName === null) return environment;
  return {
    ...environment,
    GOOS: target.goos,
    GOARCH: target.goarch,
    CGO_ENABLED: "0",
  };
}
