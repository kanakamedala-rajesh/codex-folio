import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { validateMilestoneGovernance } from "./check-milestone-governance.mjs";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const documents = {
  agents: readFileSync(join(rootDirectory, "AGENTS.md"), "utf8"),
  contract: readFileSync(join(rootDirectory, "docs", "agents", "review-contract.md"), "utf8"),
  reviewer: readFileSync(
    join(rootDirectory, ".codex", "agents", "codexfolio-milestone-reviewer.toml"),
    "utf8",
  ),
};

test("accepts completion credit while preserving separate evidence coverage", () => {
  assert.deepEqual(validateMilestoneGovernance(documents), []);
});

test("rejects governance that turns an exception into verified evidence", () => {
  const reviewer = documents.reviewer.replace("EXCEPTED = 0.0", "EXCEPTED = 1.0");
  assert.ok(
    validateMilestoneGovernance({ ...documents, reviewer }).some((failure) =>
      failure.includes("EXCEPTED = 0.0"),
    ),
  );
});

test("rejects broad waivers without exact governing permission", () => {
  const reviewer = documents.reviewer.replace(
    "Do not infer a broad waiver",
    "A broad waiver is sufficient",
  );
  assert.ok(
    validateMilestoneGovernance({ ...documents, reviewer }).some((failure) =>
      failure.includes("Do not infer a broad waiver"),
    ),
  );
});

test("rejects qualification exceptions for missing implementation", () => {
  const reviewer = documents.reviewer.replace(
    "feature is not implemented",
    "feature implementation is unavailable",
  );
  assert.ok(
    validateMilestoneGovernance({ ...documents, reviewer }).some((failure) =>
      failure.includes("feature is not implemented"),
    ),
  );
});
