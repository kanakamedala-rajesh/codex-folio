import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");

export function validateMilestoneGovernance({ agents, contract, reviewer }) {
  const failures = [];
  const requirements = [
    ["AGENTS.md", agents, "classified `VERIFIED` or validly `EXCEPTED`"],
    ["AGENTS.md", agents, "Evidence coverage may be below 100%"],
    ["review contract", contract, "governing requirement explicitly permits an exception"],
    ["review contract", contract, "missing implementation"],
    ["milestone reviewer", reviewer, "EXCEPTED = 0.0"],
    ["milestone reviewer", reviewer, "EXCEPTED = 1.0"],
    ["milestone reviewer", reviewer, "Parent Evidence Coverage"],
    ["milestone reviewer", reviewer, "Parent Completion Score"],
    ["milestone reviewer", reviewer, "Do not infer a broad waiver"],
    ["milestone reviewer", reviewer, "feature is not implemented"],
    ["milestone reviewer", reviewer, "MILESTONE PASS - 100%"],
  ];
  for (const [source, content, statement] of requirements) {
    if (!content.includes(statement)) {
      failures.push(`${source}: missing milestone-governance statement ${JSON.stringify(statement)}`);
    }
  }
  return failures;
}

function currentDocuments() {
  return {
    agents: readFileSync(join(rootDirectory, "AGENTS.md"), "utf8"),
    contract: readFileSync(join(rootDirectory, "docs", "agents", "review-contract.md"), "utf8"),
    reviewer: readFileSync(
      join(rootDirectory, ".codex", "agents", "codexfolio-milestone-reviewer.toml"),
      "utf8",
    ),
  };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const failures = validateMilestoneGovernance(currentDocuments());
  if (failures.length > 0) {
    console.error(`milestone governance check failed:\n${failures.join("\n")}`);
    process.exitCode = 1;
  } else {
    console.log("milestone governance check passed: exceptions remain distinct from evidence");
  }
}
