import { execFileSync } from "node:child_process";
import { lstatSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

// Conservative project-specific hygiene checks, not a YAML security parser or
// a secret scanner. Full-history and hosted-content review remain separate.
const approvedWorkflows = new Set([
  ".github/workflows/verify.yml",
  ".github/workflows/dco.yml",
]);
const approvedActionNames = new Set([
  "actions/checkout",
  "actions/setup-go",
  "actions/setup-node",
]);
const safeEnvExamples = new Set([".env.example", ".env.sample", ".env.template"]);

export function pathHazard(path) {
  const name = path.split("/").at(-1);
  if ((name === ".env" || name.startsWith(".env.")) && !safeEnvExamples.has(name)) {
    return "environment file";
  }
  if (/^(auth|credentials|tokens?)\.json$/i.test(name)) return "credential-shaped file";
  if (/\.(?:sqlite3?|db)(?:-(?:wal|shm))?$/i.test(name)) return "local database";
  if (/^(?:\.codex\/(?:sessions|archived_sessions|logs?)\/|identity-homes\/|local-state\/|publication-audit\/)/.test(path)) {
    return "private runtime or audit directory";
  }
  if (/^(?:diagnostic-bundle|gitleaks-report)[^/]*$/i.test(path)) return "private report";
  return null;
}

export function workflowFindings(path, content) {
  const errors = [];
  const text = content.split(/\r?\n/).filter((line) => !/^\s*#/.test(line)).join("\n");
  if (!approvedWorkflows.has(path)) errors.push("new workflow needs an explicit policy review");
  if (/(?:^|[\s\[{},])['"]?(?:pull_request_target|workflow_run)['"]?\s*(?::|[,\]}])/m.test(text)) {
    errors.push("privileged event is outside the approved PR policy");
  }
  if (/^\s*permissions:\s*write-all\b/m.test(text) || /^\s*[a-z][a-z-]*:\s*write\b/m.test(text)) {
    errors.push("write permissions are outside the approved PR policy");
  }
  if (/^\s*secrets:\s*inherit\b/m.test(text)) errors.push("inherited secrets need a separate release design");
  if (/\$\{\{[^}]*\bsecrets\s*[.\[]/.test(text)) errors.push("application secrets are outside the approved PR policy");
  if (!/^permissions:\s*\n\s+contents:\s*read\s*$/m.test(text)) errors.push("explicit read-only permissions are missing");
  if (!/^concurrency:\s*$/m.test(text)) errors.push("workflow concurrency is missing");
  const timeouts = [...text.matchAll(/^\s+timeout-minutes:\s*(\d+)\s*$/gm)];
  if (timeouts.length === 0 || timeouts.some((match) => Number(match[1]) < 1 || Number(match[1]) > 60)) {
    errors.push("an explicit timeout in the range 1 to 60 minutes is required");
  }
  for (const match of text.matchAll(/^\s*(?:-\s*)?uses:\s*(\S+)/gm)) {
    const value = match[1];
    const split = value.lastIndexOf("@");
    if (split < 0 || !approvedActionNames.has(value.slice(0, split)) || !/^[0-9a-f]{40}$/i.test(value.slice(split + 1))) {
      errors.push("an action name or immutable revision needs review");
    }
  }
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i += 1) {
    if (!/uses:\s*actions\/checkout@/.test(lines[i])) continue;
    const step = [];
    for (let j = i + 1; j < lines.length && !/^      - /.test(lines[j]); j += 1) step.push(lines[j]);
    if (!step.some((line) => /^\s+persist-credentials:\s*false\s*$/.test(line))) {
      errors.push("checkout must not persist credentials");
    }
  }
  if (path.endsWith("/verify.yml")) {
    const labels = [...text.matchAll(/^\s+runner:\s*(\S+)\s*$/gm)].map((m) => m[1]).sort();
    if (JSON.stringify(labels) !== JSON.stringify(["macos-15", "ubuntu-latest", "windows-latest"])) {
      errors.push("the reviewed three-platform standard-runner matrix changed");
    }
    const runners = [...text.matchAll(/^\s+runs-on:\s*(.+)$/gm)].map((m) => m[1].trim());
    if (JSON.stringify(runners) !== JSON.stringify(["${{ matrix.runner }}", "ubuntu-24.04"])) errors.push("unexpected runner selection");
    if (!text.includes("cancel-in-progress: ${{ github.event_name == 'pull_request' }}")) errors.push("main verification must not be actively cancelled by PR cancellation policy");
    if (!text.includes("go.sum")) errors.push("Go dependency checksums are missing from the cache key inputs");
    const mask = text.indexOf('echo "::add-mask::$keychain_password"');
    const use = text.indexOf('security create-keychain -p "$keychain_password"');
    if (mask < 0 || use < 0 || mask > use) errors.push("mask the ephemeral Keychain password before use");
  }
  if (path.endsWith("/dco.yml")) {
    if (!text.includes('git show "${DCO_BASE}:scripts/check-dco.mjs"')) errors.push("DCO must read the checker from the base revision");
    const runners = [...text.matchAll(/^\s+runs-on:\s*(.+)$/gm)].map((m) => m[1].trim());
    if (JSON.stringify(runners) !== JSON.stringify(["ubuntu-latest"])) errors.push("DCO must retain its standard Ubuntu runner");
  }
  return [...new Set(errors)];
}

export function auditRepository(root) {
  const records = execFileSync("git", ["ls-files", "--stage", "-z"], { cwd: root, encoding: "utf8" }).split("\0").filter(Boolean);
  const findings = [];
  const seen = new Set();
  for (const record of records) {
    const tab = record.indexOf("\t");
    const [mode, , stage] = record.slice(0, tab).split(" ");
    const path = record.slice(tab + 1);
    if (stage !== "0") { findings.push(`${path}: resolve the unmerged index first`); continue; }
    if (seen.has(path)) continue;
    seen.add(path);
    if (mode === "120000" || mode === "160000") { findings.push(`${path}: symlink or submodule requires a separate publication review`); continue; }
    const hazard = pathHazard(path);
    if (hazard) findings.push(`${path}: ${hazard}; use a reviewed synthetic fixture name, not a runtime filename`);
    if (!/^\.github\/workflows\/[^/]+\.ya?ml$/.test(path)) continue;
    const file = join(root, path);
    if (!lstatSync(file).isFile()) { findings.push(`${path}: expected a regular workflow file`); continue; }
    findings.push(...workflowFindings(path, readFileSync(file, "utf8")).map((item) => `${path}: ${item}`));
  }
  for (const required of approvedWorkflows) if (!seen.has(required)) findings.push(`${required}: required workflow missing from the index`);
  return findings;
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
    const findings = auditRepository(root);
    if (findings.length) {
      console.error(findings.join("\n"));
      process.exitCode = 1;
    } else {
      console.log("publication hygiene passed for indexed paths and current workflows; history, secrets, and hosted content are not certified");
    }
  } catch {
    console.error("publication hygiene could not complete; inspect the Git index and workflow files locally");
    process.exitCode = 2;
  }
}
