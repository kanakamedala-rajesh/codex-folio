import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { pathHazard, workflowFindings } from "./check-public-safety.mjs";

const verify = readFileSync(fileURLToPath(new URL("../.github/workflows/verify.yml", import.meta.url)), "utf8");
const dco = readFileSync(fileURLToPath(new URL("../.github/workflows/dco.yml", import.meta.url)), "utf8");

test("versioned project agents and reviewed env example names remain allowed", () => {
  for (const path of [".codex/agents/reviewer.toml", "internal/profile/auth_test.go", ".env.example", "web/.env.sample", "api/fixtures/metadata-response.json"]) assert.equal(pathHazard(path), null);
});
test("credential and runtime paths are flagged without reading secret values", () => {
  for (const path of [".env", "web/.env.production", ".codex/auth.json", "data/private.sqlite-wal", "local-state/settings.json", "publication-audit/result.json", "diagnostic-bundle.zip"]) assert.ok(pathHazard(path));
});
test("the reviewed workflows satisfy the project-specific guard", () => {
  assert.deepEqual(workflowFindings(".github/workflows/verify.yml", verify), []);
  assert.deepEqual(workflowFindings(".github/workflows/dco.yml", dco), []);
});
test("a floating action version is rejected", () => {
  assert.ok(workflowFindings(".github/workflows/verify.yml", verify.replace(/actions\/checkout@[a-f0-9]{40}/, "actions/checkout@main")).some((s) => s.includes("immutable")));
});
test("persisted checkout credentials are rejected", () => {
  assert.ok(workflowFindings(".github/workflows/verify.yml", verify.replace("persist-credentials: false", "persist-credentials: true")).some((s) => s.includes("persist")));
});
test("a larger runner is rejected", () => {
  assert.ok(workflowFindings(".github/workflows/verify.yml", verify.replace("runner: macos-15", "runner: macos-15-large")).some((s) => s.includes("standard-runner")));
});
test("the shared web job retains its reviewed runner", () => {
  for (const replacement of ["runs-on: self-hosted", ""]) {
    assert.ok(workflowFindings(".github/workflows/verify.yml", verify.replace("runs-on: ubuntu-24.04", replacement)).some((s) => s.includes("runner selection")));
  }
});
test("write permissions and privileged events are rejected", () => {
  const changed = verify.replace("contents: read", "contents: write").replace("  pull_request:", "  pull_request_target:");
  const errors = workflowFindings(".github/workflows/verify.yml", changed);
  assert.ok(errors.some((s) => s.includes("write permissions")));
  assert.ok(errors.some((s) => s.includes("privileged event")));
});
test("missing or excessive timeout is rejected", () => {
  assert.ok(workflowFindings(".github/workflows/verify.yml", verify.replace("timeout-minutes: 60", "timeout-minutes: 360")).some((s) => s.includes("timeout")));
});
test("DCO checker changes in the proposed tree cannot change the extraction policy", () => {
  assert.ok(workflowFindings(".github/workflows/dco.yml", dco.replace('git show "${DCO_BASE}:scripts/check-dco.mjs"', 'cat scripts/check-dco.mjs')).some((s) => s.includes("base revision")));
});
test("a new workflow requires explicit review", () => {
  assert.ok(workflowFindings(".github/workflows/release.yml", dco).some((s) => s.includes("new workflow")));
});
test("removing one platform is not treated as a cost optimization", () => {
  assert.ok(workflowFindings(".github/workflows/verify.yml", verify.replace("            runner: windows-latest\n", "")).some((s) => s.includes("three-platform")));
});
