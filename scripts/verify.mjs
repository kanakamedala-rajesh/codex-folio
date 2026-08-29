import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const webDirectory = join(rootDirectory, "web");
const expectedGoVersion = "go1.27.0";
const goPackages = ["./cmd/...", "./internal/..."];

try {
  const initialStatus = gitStatus();
  const initialTrackedFiles = trackedFileSnapshot();

  checkGoToolchain();
  run("npm", ["--prefix", "web", "run", "check:tooling"]);

  if (!existsSync(join(webDirectory, "node_modules"))) {
    throw new Error("frontend dependencies are missing; run npm --prefix web ci first");
  }

  run("node", ["scripts/generate-openapi.mjs", "--check"]);
  run("node", ["--test", "scripts/generate-openapi.test.mjs"]);
  run("node", ["scripts/check-architecture.mjs"]);
  run("node", ["--test", "scripts/check-architecture.test.mjs"]);
  run("node", ["scripts/check-error-codes.mjs"]);
  run("node", ["--test", "scripts/check-error-codes.test.mjs"]);

  const goFiles = findGoFiles(rootDirectory);
  const formattedGoFiles = goFiles.length === 0 ? "" : capture("gofmt", ["-l", ...goFiles]);
  if (formattedGoFiles.trim() !== "") {
    throw new Error(`gofmt found unformatted Go files:\n${formattedGoFiles.trim()}`);
  }

  run("go", ["vet", ...goPackages]);
  run("go", ["test", ...goPackages]);
  run("node", ["scripts/build.mjs", "--build-class", "development"]);
  run("node", ["--test", "scripts/build-targets.test.mjs"]);
  run("node", ["--test", "scripts/release.test.mjs"]);
  run("node", ["scripts/release.mjs", "--dry-run", "--build-class", "development"]);
  run("node", ["--test", "scripts/check-dco.test.mjs"]);
  run("node", ["scripts/check-governance.mjs"]);

  run("npm", ["--prefix", "web", "run", "format:check"]);
  run("npm", ["--prefix", "web", "run", "lint"]);
  run("npm", ["--prefix", "web", "run", "typecheck"]);
  run("npm", ["--prefix", "web", "run", "test"]);
  run("npm", ["--prefix", "web", "run", "build"]);
  run("node", ["web/scripts/smoke.mjs"]);

  const finalStatus = gitStatus();
  const finalTrackedFiles = trackedFileSnapshot();
  if (initialStatus !== finalStatus) {
    throw new Error(
      "verification changed the working-tree status; generated output must stay ignored",
    );
  }
  if (JSON.stringify(initialTrackedFiles) !== JSON.stringify(finalTrackedFiles)) {
    throw new Error("verification changed tracked source files");
  }

  console.log(
    "verification passed: Go, frontend, and governance checks left tracked source unchanged",
  );
} catch (error) {
  console.error(`verification failed: ${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
}

function checkGoToolchain() {
  const version = capture("go", ["version"]).match(/\bgo\d+\.\d+(?:\.\d+)?\b/)?.[0];
  if (version !== expectedGoVersion) {
    throw new Error(
      `Go ${expectedGoVersion} is required; found ${version ?? "unavailable"}. See .tool-versions.`,
    );
  }
}

function capture(command, args) {
  const result = spawnSync(command, args, {
    cwd: rootDirectory,
    env: taskEnvironment(),
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(
      `${command} exited with status ${result.status ?? "unknown"}: ${result.stderr.trim()}`,
    );
  }
  return result.stdout;
}

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: rootDirectory,
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

function gitStatus() {
  return capture("git", ["status", "--porcelain=v1", "--untracked-files=all"]);
}

function findGoFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    if (entry.isDirectory() && [".git", "build", "dist", "node_modules"].includes(entry.name)) {
      return [];
    }
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      return findGoFiles(path);
    }
    return entry.name.endsWith(".go") ? [relative(rootDirectory, path)] : [];
  });
}

function trackedFileSnapshot() {
  const files = capture("git", ["ls-files", "-z"]).split("\0").filter(Boolean);
  return files.map((file) => [
    file,
    createHash("sha256")
      .update(readFileSync(resolve(rootDirectory, file)))
      .digest("hex"),
  ]);
}

function taskEnvironment() {
  return {
    ...process.env,
    GOCACHE: process.env.GOCACHE ?? join(tmpdir(), "codex-folio-go-cache"),
  };
}
