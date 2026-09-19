import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const importLister = resolve(rootDirectory, "scripts/list-go-imports.go");
const outwardAdapters = new Set([
  "internal/adapters",
  "internal/httpapi",
  "internal/platform",
  "internal/store",
]);
const featureModules = new Set([
  "internal/activity",
  "internal/alerts",
  "internal/apperrors",
  "internal/application",
  "internal/configpack",
  "internal/continuation",
  "internal/diagnostics",
  "internal/launch",
  "internal/profile",
  "internal/settings",
  "internal/sharedhome",
  "internal/usage",
  "internal/updates",
]);
const supportPackages = new Set(["internal/buildinfo", "internal/vault"]);

export function architectureFailures(root = rootDirectory) {
  const modulePath = readModulePath(join(root, "go.mod"));
  const failures = [];

  for (const { packagePath, imports } of sourcePackages(root)) {
    const packageLayer = layerOf(packagePath);
    if (packageLayer === "unclassified") {
      failures.push(
        `${packagePath}: internal package root must be classified as a feature, adapter, or support package in scripts/check-architecture.mjs`,
      );
      continue;
    }
    if (packageLayer === "composition" || packageLayer === "other") continue;

    for (const importPath of imports) {
      if (!importPath.startsWith(`${modulePath}/`)) continue;
      const importedPackage = localPackagePath(importPath, modulePath);
      const importedLayer = layerOf(importedPackage);
      if (importedLayer !== "adapter") continue;

      if (packageLayer === "feature" || packageLayer === "support") {
        const label =
          packageLayer === "feature" ? "feature module" : "support package";
        failures.push(
          `${packagePath}: ${label} must not import outward adapter ${importedPackage}`,
        );
      } else if (
        packageLayer === "adapter" &&
        adapterRoot(packagePath) !== adapterRoot(importedPackage)
      ) {
        failures.push(
          `${packagePath}: outward adapter must not import outward adapter ${importedPackage}`,
        );
      }
    }
  }

  return failures.sort();
}

function layerOf(packagePath) {
  if (packagePath === "cmd" || packagePath.startsWith("cmd/"))
    return "composition";
  if (adapterRoot(packagePath)) return "adapter";
  if (packageRoot(packagePath, featureModules)) return "feature";
  if (packageRoot(packagePath, supportPackages)) return "support";
  if (packagePath.startsWith("internal/")) return "unclassified";
  return "other";
}

function adapterRoot(packagePath) {
  return [...outwardAdapters].find(
    (adapter) =>
      packagePath === adapter || packagePath.startsWith(`${adapter}/`),
  );
}

function readModulePath(path) {
  const match = readFileSync(path, "utf8").match(/^module\s+(\S+)\s*$/m);
  if (!match) throw new Error("go.mod must declare a module path");
  return match[1];
}

function sourcePackages(root) {
  const result = spawnSync(
    "go",
    ["run", "-buildvcs=false", importLister, root],
    {
      cwd: rootDirectory,
      encoding: "utf8",
    },
  );
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`Go import parser failed: ${result.stderr.trim()}`);
  }
  return JSON.parse(result.stdout);
}

function localPackagePath(importPath, modulePath) {
  return importPath === modulePath
    ? "."
    : importPath.slice(modulePath.length + 1);
}

function packageRoot(packagePath, roots) {
  return [...roots].find(
    (root) => packagePath === root || packagePath.startsWith(`${root}/`),
  );
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const failures = architectureFailures();
    if (failures.length > 0) throw new Error(failures.join("\n"));
    console.log("architecture check passed: Go dependencies point inward");
  } catch (error) {
    console.error(
      `architecture check failed:\n${error instanceof Error ? error.message : String(error)}`,
    );
    process.exitCode = 1;
  }
}
