import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const checker = resolve("scripts/check-dco.mjs");

test("accepts commits with a matching DCO sign-off", () => {
  const repository = repositoryWithCommit(
    "docs: add policy\n\nSigned-off-by: Example Contributor <contributor@example.com>",
  );

  const result = runChecker(repository);

  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /DCO check passed: 1 commit/);
});

test("rejects commits without a matching DCO sign-off", () => {
  const repository = repositoryWithCommit("docs: add policy");

  const result = runChecker(repository);

  assert.equal(result.status, 1);
  assert.match(result.stderr, /missing a matching Signed-off-by trailer/);
});

test("rejects a Signed-off-by example outside the trailer block", () => {
  const repository = repositoryWithCommit(
    "docs: explain sign-off\n\nSigned-off-by: Example Contributor <contributor@example.com>\n\nThis sentence is part of the message body.",
  );

  const result = runChecker(repository);

  assert.equal(result.status, 1);
  assert.match(result.stderr, /missing a matching Signed-off-by trailer/);
});

function repositoryWithCommit(message) {
  const directory = mkdtempSync(join(tmpdir(), "codex-folio-dco-"));
  execFileSync("git", ["init", "--quiet"], { cwd: directory });
  execFileSync("git", ["config", "user.name", "Example Contributor"], {
    cwd: directory,
  });
  execFileSync("git", ["config", "user.email", "contributor@example.com"], {
    cwd: directory,
  });
  writeFileSync(join(directory, "policy.md"), "governance\n");
  execFileSync("git", ["add", "policy.md"], { cwd: directory });
  execFileSync("git", ["commit", "--quiet", "--message", message], {
    cwd: directory,
  });
  return directory;
}

function runChecker(repository) {
  return spawnSync(process.execPath, [checker, "--range", "HEAD"], {
    cwd: repository,
    encoding: "utf8",
  });
}
