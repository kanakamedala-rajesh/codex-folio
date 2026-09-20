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
const passedGates = [];
const skippedQualifications = [
  "native runtime qualification for Tier 1 target builds (compile-only in Phase 0)",
  "stable signing, attestation, notarization, and release eligibility (not implemented in Phase 0)",
];

try {
  const args = process.argv.slice(2);
  const suite =
    args.length === 0
      ? "all"
      : args.length === 2 && args[0] === "--suite"
        ? args[1]
        : "";
  if (!["all", "native", "web"].includes(suite)) {
    throw new Error("usage: node scripts/verify.mjs [--suite all|native|web]");
  }
  const native = suite !== "web";
  const web = suite !== "native";
  console.log(`Verification suite: ${suite}`);
  if (!native)
    skippedQualifications.push(
      "native checks belong to the separate native suite",
    );
  if (!web)
    skippedQualifications.push(
      "deep UI and startup budget belong to the separate web suite",
    );
  const initialStatus = gitStatus();
  const initialTrackedFiles = trackedFileSnapshot();

  gate("publication hygiene guardrails", () => {
    run("node", ["--test", "scripts/check-public-safety.test.mjs"]);
    run("node", ["scripts/check-public-safety.mjs"]);
  });
  gate("pinned Go, Node.js, and npm toolchains", () => {
    checkGoToolchain();
    runNpm(["--prefix", "web", "run", "check:tooling"]);
  });
  gate("locked frontend dependency installation", () => {
    runNpm(["--prefix", "web", "ci"]);
    if (!existsSync(join(webDirectory, "node_modules"))) {
      throw new Error("npm ci completed without creating web/node_modules");
    }
  });
  gate("OpenAPI generation and drift", () => {
    run("node", ["scripts/generate-openapi.mjs", "--check"]);
    run("node", ["--test", "scripts/generate-openapi.test.mjs"]);
  });
  gate("architecture dependency direction", () => {
    run("node", ["scripts/check-architecture.mjs"]);
    run("node", ["--test", "scripts/check-architecture.test.mjs"]);
  });
  gate("stable error-code registry", () => {
    run("node", ["scripts/check-error-codes.mjs"]);
    run("node", ["--test", "scripts/check-error-codes.test.mjs"]);
  });
  if (native) {
    gate("Go formatting, linting, and unit tests", () => {
      const goFiles = findGoFiles(rootDirectory);
      const formattedGoFiles =
        goFiles.length === 0 ? "" : capture("gofmt", ["-l", ...goFiles]);
      if (formattedGoFiles.trim() !== "") {
        throw new Error(
          `gofmt found unformatted Go files:\n${formattedGoFiles.trim()}`,
        );
      }
      run("go", ["vet", ...goPackages]);
      run("go", ["test", ...goPackages]);
    });
    gate("native executable build identity", () => {
      run("node", ["scripts/build.mjs", "--build-class", "development"]);
      checkBuiltExecutableIdentity(initialStatus);
    });
    gate("Tier 1 compile-only target builds", () => {
      run("node", ["--test", "scripts/build-targets.test.mjs"]);
    });
    gate("unsigned archive and supply-chain dry run", () => {
      run("node", ["--test", "scripts/release.test.mjs"]);
      run("node", [
        "scripts/release.mjs",
        "--dry-run",
        "--build-class",
        "development",
      ]);
    });
  }
  gate("governance and DCO policy", () => {
    run("node", ["--test", "scripts/check-dco.test.mjs"]);
    run("node", ["--test", "scripts/check-milestone-governance.test.mjs"]);
    run("node", ["scripts/check-milestone-governance.mjs"]);
    run("node", ["scripts/check-governance.mjs"]);
  });
  if (web) {
    gate(
      "frontend format, lint, type-check, test, build, and offline assets",
      () => {
        runNpm(["--prefix", "web", "run", "format:check"]);
        runNpm(["--prefix", "web", "run", "lint"]);
        runNpm(["--prefix", "web", "run", "typecheck"]);
        runNpm(["--prefix", "web", "run", "test"]);
        run("node", ["web/scripts/smoke.mjs", "internal/httpapi/assets"]);
      },
    );
  } else {
    gate(
      "embedded frontend build and offline assets for native integration",
      () => {
        runNpm(["--prefix", "web", "run", "build"]);
        run("node", ["web/scripts/smoke.mjs", "internal/httpapi/assets"]);
      },
    );
  }
  gate("pinned browser installation", () => {
    if (!process.env.CODEX_FOLIO_CHROMIUM) {
      run("node", [
        "web/node_modules/playwright/cli.js",
        "install",
        "chromium",
        ...(process.env.CI ? ["--with-deps"] : []),
      ]);
    }
  });
  if (native) {
    gate("native browser-to-service smoke", () => {
      run(
        "go",
        [
          "test",
          "./cmd/codex-folio",
          "-run",
          "^TestOverviewBrowser$",
          "-count=1",
          "-v",
          "-timeout=5m",
        ],
        { CODEX_FOLIO_BROWSER_TEST: "1", CODEX_FOLIO_BROWSER_SUITE: "smoke" },
      );
    });
  }
  if (web) {
    gate("deep authenticated Overview journeys and accessibility", () => {
      run(
        "go",
        [
          "test",
          "./cmd/codex-folio",
          "-run",
          "^TestOverviewBrowser$",
          "-count=1",
          "-v",
          "-timeout=5m",
        ],
        { CODEX_FOLIO_BROWSER_TEST: "1", CODEX_FOLIO_BROWSER_SUITE: "deep" },
      );
    });
    gate("isolated repeated startup benchmark", () => {
      run(
        "go",
        [
          "test",
          "./cmd/codex-folio",
          "-run",
          "^TestOverviewStartupBenchmark$",
          "-count=1",
          "-v",
          "-timeout=5m",
        ],
        { CODEX_FOLIO_BROWSER_TEST: "1" },
      );
    });
  }
  gate("tracked source and lockfile immutability", () => {
    const finalStatus = gitStatus();
    const finalTrackedFiles = trackedFileSnapshot();
    if (initialStatus !== finalStatus) {
      throw new Error(
        "verification changed the working-tree status; generated output must stay ignored",
      );
    }
    if (
      JSON.stringify(initialTrackedFiles) !== JSON.stringify(finalTrackedFiles)
    ) {
      throw new Error("verification changed tracked source files");
    }
  });

  console.log("\nRepository verification summary");
  for (const name of passedGates) console.log(`[PASS] ${name}`);
  for (const name of skippedQualifications) console.log(`[SKIP] ${name}`);
  console.log(
    `verification passed: ${suite} suite completed without remote mutation`,
  );
} catch (error) {
  console.error(
    `verification failed: ${error instanceof Error ? error.message : String(error)}`,
  );
  process.exitCode = 1;
}

function gate(name, check) {
  try {
    check();
    passedGates.push(name);
    console.log(`[PASS] ${name}`);
  } catch (error) {
    console.error(`[FAIL] ${name}`);
    throw error;
  }
}

function checkGoToolchain() {
  const version = capture("go", ["version"]).match(
    /\bgo\d+\.\d+(?:\.\d+)?\b/,
  )?.[0];
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

function checkBuiltExecutableIdentity(initialStatus) {
  const executable =
    process.platform === "win32" ? "codex-folio.exe" : "codex-folio";
  const output = capture(join(rootDirectory, "build", "bin", executable), [
    "version",
    "--json",
  ]);
  let metadata;
  try {
    metadata = JSON.parse(output);
  } catch (error) {
    throw new Error(
      `built executable returned invalid version JSON: ${error instanceof Error ? error.message : String(error)}`,
    );
  }

  const expected = {
    product: "VenkataSudha CodexFolio",
    command: "codex-folio",
    version: readFileSync(
      join(rootDirectory, "internal", "buildinfo", "version.txt"),
      "utf8",
    ).trim(),
    source_revision: capture("git", ["rev-parse", "--verify", "HEAD"]).trim(),
    build_class: "development",
    dirty: initialStatus === "" ? "clean" : "dirty",
  };
  for (const [field, value] of Object.entries(expected)) {
    if (metadata[field] !== value) {
      throw new Error(
        `built executable version field ${field} is ${JSON.stringify(metadata[field])}; expected ${JSON.stringify(value)}`,
      );
    }
  }
  console.log(
    `executable identity: ${metadata.version} ${metadata.source_revision} ${metadata.build_class} ${metadata.dirty}`,
  );
}

function run(command, args, environment = {}) {
  const result = spawnSync(command, args, {
    cwd: rootDirectory,
    env: { ...taskEnvironment(), ...environment },
    stdio: "inherit",
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(
      `${command} exited with status ${result.status ?? "unknown"}`,
    );
  }
}

function runNpm(args) {
  if (process.platform === "win32") {
    run(process.env.ComSpec ?? "cmd.exe", [
      "/d",
      "/s",
      "/c",
      "npm.cmd",
      ...args,
    ]);
    return;
  }

  run("npm", args);
}

function gitStatus() {
  return capture("git", ["status", "--porcelain=v1", "--untracked-files=all"]);
}

function findGoFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    if (
      entry.isDirectory() &&
      [".git", "build", "dist", "node_modules"].includes(entry.name)
    ) {
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
