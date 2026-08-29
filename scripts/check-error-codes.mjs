import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const registryPath = resolve(rootDirectory, "internal/apperrors/codes.json");
const goConstantsPath = resolve(rootDirectory, "internal/apperrors/codes.go");
const codePattern = /^CF_[A-Z][A-Z0-9]*_[A-Z][A-Z0-9_]*$/;
const ownerPattern = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;
const semverPattern =
  /^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-(?:(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const lifecycles = new Set(["active", "deprecated", "retired"]);
const allowedFields = new Set([
  "code",
  "owner",
  "lifecycle",
  "introduced_in",
  "replacement",
  "retired_in",
]);

export function errorCodeFailures(registry) {
  const failures = [];
  if (!registry || typeof registry !== "object" || Array.isArray(registry)) {
    return ["registry: must be a JSON object"];
  }
  if (registry.version !== 1) failures.push("version: must be 1");
  if (!Array.isArray(registry.codes))
    return [...failures, "codes: must be an array"];

  const firstIndexByCode = new Map();
  registry.codes.forEach((entry, index) => {
    const path = `codes[${index}]`;
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) {
      failures.push(`${path}: must be an object`);
      return;
    }

    if (!codePattern.test(entry.code ?? "")) {
      failures.push(
        `${path}.code: ${entry.code ?? "undefined"} must match CF_<OWNER>_<CONDITION>`,
      );
    } else if (firstIndexByCode.has(entry.code)) {
      failures.push(
        `${path}.code: ${entry.code} duplicates codes[${firstIndexByCode.get(entry.code)}]; retired identifiers cannot be reused`,
      );
    } else {
      firstIndexByCode.set(entry.code, index);
    }

    if (!ownerPattern.test(entry.owner ?? "")) {
      failures.push(`${path}.owner: must be a lowercase module owner`);
    } else if (codePattern.test(entry.code ?? "")) {
      const ownerPrefix = `CF_${entry.owner.toUpperCase().replaceAll("-", "_")}_`;
      if (!entry.code.startsWith(ownerPrefix)) {
        failures.push(
          `${path}.code: ${entry.code} must use owner prefix ${ownerPrefix}`,
        );
      }
    }
    if (!lifecycles.has(entry.lifecycle)) {
      failures.push(
        `${path}.lifecycle: ${entry.lifecycle ?? "undefined"} must be active, deprecated, or retired`,
      );
    }
    if (!semverPattern.test(entry.introduced_in ?? "")) {
      failures.push(
        `${path}.introduced_in: ${entry.introduced_in ?? "undefined"} must be a semantic product version`,
      );
    }
    if (
      entry.lifecycle === "retired" &&
      !semverPattern.test(entry.retired_in ?? "")
    ) {
      failures.push(
        `${path}.retired_in: retired code ${entry.code ?? "undefined"} requires a semantic product version`,
      );
    }
    if (entry.lifecycle !== "retired" && "retired_in" in entry) {
      failures.push(
        `${path}.retired_in: only retired codes may declare retirement`,
      );
    }
    if (
      entry.lifecycle === "deprecated" &&
      !codePattern.test(entry.replacement ?? "")
    ) {
      failures.push(
        `${path}.replacement: deprecated code ${entry.code ?? "undefined"} requires a valid replacement code`,
      );
    }
    for (const field of Object.keys(entry)) {
      if (!allowedFields.has(field)) {
        failures.push(
          field === "message"
            ? `${path}.message: user-facing copy must remain outside the stable error-code registry`
            : `${path}.${field}: field is not allowed`,
        );
      }
    }
  });

  const knownCodes = new Set(firstIndexByCode.keys());
  registry.codes.forEach((entry, index) => {
    if (
      entry?.lifecycle === "deprecated" &&
      !knownCodes.has(entry.replacement)
    ) {
      failures.push(
        `codes[${index}].replacement: ${entry.replacement} is not registered`,
      );
    }
  });

  return failures;
}

export function goConstantFailures(source, registry) {
  const failures = [];
  const registryCodes = new Set(
    (registry.codes ?? []).map((entry) => entry.code),
  );
  const constantCodes = [
    ...source.matchAll(/\b[A-Z][A-Za-z0-9]*\s*=\s*"(CF_[A-Z0-9_]+)"/g),
  ].map((match) => match[1]);
  const seenConstants = new Set();

  for (const code of constantCodes) {
    if (seenConstants.has(code)) {
      failures.push(`codes.go: ${code} is declared more than once`);
    } else if (!registryCodes.has(code)) {
      failures.push(`codes.go: ${code} is not in codes.json`);
    }
    seenConstants.add(code);
  }
  for (const code of registryCodes) {
    if (!seenConstants.has(code)) {
      failures.push(`codes.json: ${code} has no Go constant`);
    }
  }
  return failures;
}

export function historicalRetiredCodeFailures(registry, historicalRegistries) {
  const currentByCode = new Map(
    (registry.codes ?? []).map((entry) => [entry.code, entry]),
  );
  const retiredCodes = new Set(
    historicalRegistries.flatMap((historical) =>
      (historical.codes ?? [])
        .filter((entry) => entry.lifecycle === "retired")
        .map((entry) => entry.code),
    ),
  );

  return [...retiredCodes].sort().flatMap((code) => {
    const current = currentByCode.get(code);
    if (!current) {
      return [`codes.json: retired identifier ${code} must remain registered`];
    }
    if (current.lifecycle !== "retired") {
      return [
        `codes.json: historically retired identifier ${code} cannot become ${current.lifecycle}`,
      ];
    }
    return [];
  });
}

function registryHistory(root, path) {
  const repositoryPath = relative(root, path);
  const revisions = runGit(root, ["rev-list", "HEAD", "--", repositoryPath])
    .trim()
    .split("\n")
    .filter(Boolean);
  return revisions.map((revision) =>
    JSON.parse(runGit(root, ["show", `${revision}:${repositoryPath}`])),
  );
}

function runGit(root, args) {
  const result = spawnSync("git", args, { cwd: root, encoding: "utf8" });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`git ${args[0]} failed: ${result.stderr.trim()}`);
  }
  return result.stdout;
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const registry = JSON.parse(readFileSync(registryPath, "utf8"));
    const failures = [
      ...errorCodeFailures(registry),
      ...goConstantFailures(readFileSync(goConstantsPath, "utf8"), registry),
      ...historicalRetiredCodeFailures(
        registry,
        registryHistory(rootDirectory, registryPath),
      ),
    ];
    if (failures.length > 0) throw new Error(failures.join("\n"));
    console.log(
      `error-code check passed: ${registry.codes.length} stable identifiers are valid`,
    );
  } catch (error) {
    console.error(
      `error-code check failed:\n${error instanceof Error ? error.message : String(error)}`,
    );
    process.exitCode = 1;
  }
}
