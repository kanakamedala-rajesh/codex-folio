import assert from "node:assert/strict";
import {
  copyFileSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import { checkOpenAPI } from "./generate-openapi.mjs";

const repositoryDirectory = resolve(".");
const generatedFiles = [
  "internal/httpapi/openapi.gen.go",
  "web/src/generated/openapi.ts",
];

test("the checked-in OpenAPI artifacts match the canonical contract", () => {
  assert.doesNotThrow(() => checkOpenAPI(repositoryDirectory));
});

test("the generated usage client exposes safe error identifiers", async () => {
  const { createCodexFolioApiClient, UsageRefreshError } = await import("../web/src/generated/openapi.ts");
  const client = createCodexFolioApiClient("", async () => new Response(
    JSON.stringify({ code: "CF_USAGE_PROFILE_UNAVAILABLE", message: "Usage is unavailable for this profile." }),
    { status: 409, headers: { "Content-Type": "application/json" } },
  ));

  await assert.rejects(
    client.refreshUsage({ alias: "Work" }),
    (error) => error instanceof UsageRefreshError
      && error.code === "CF_USAGE_PROFILE_UNAVAILABLE"
      && error.message === "Usage is unavailable for this profile.",
  );
});

test("the generated analytics readers preserve expired-session status", async () => {
  const { createCodexFolioApiClient, UsageRefreshError } = await import("../web/src/generated/openapi.ts");
  const client = createCodexFolioApiClient("", async () => new Response(
    JSON.stringify({ code: "CF_HTTPAPI_FORBIDDEN", message: "Browser authorization expired." }),
    { status: 403, headers: { "Content-Type": "application/json" } },
  ));

  for (const read of [() => client.getActivity(), () => client.getProjects()]) {
    await assert.rejects(
      read(),
      (error) => error instanceof UsageRefreshError
        && error.code === "CF_HTTPAPI_FORBIDDEN"
        && error.status === 403,
    );
  }
});

test("the OpenAPI check rejects a generated artifact that drifted", (t) => {
  const fixtureDirectory = mkdtempSync(join(tmpdir(), "codex-folio-openapi-"));
  t.after(() => rmSync(fixtureDirectory, { force: true, recursive: true }));
  copyRepositoryInputs(fixtureDirectory);

  const generatedGoPath = join(fixtureDirectory, generatedFiles[0]);
  const generatedGo = readFileSync(generatedGoPath, "utf8");
  writeFileSync(
    generatedGoPath,
    generatedGo.replace(
      /(ContractSourceSHA256\s+= ")/,
      "$1tampered-",
    ),
  );

  assert.throws(() => checkOpenAPI(fixtureDirectory), /drift/i);
});

test("the contract version follows the canonical product version", (t) => {
  const fixtureDirectory = mkdtempSync(join(tmpdir(), "codex-folio-openapi-"));
  t.after(() => rmSync(fixtureDirectory, { force: true, recursive: true }));
  copyRepositoryInputs(fixtureDirectory);
  writeFileSync(join(fixtureDirectory, "internal/buildinfo/version.txt"), "9.9.9\n");

  assert.throws(() => checkOpenAPI(fixtureDirectory), /canonical product version/i);
});

function copyRepositoryInputs(destination) {
  for (const relativePath of [
    "api/openapi.json",
    "internal/buildinfo/version.txt",
    ...generatedFiles,
  ]) {
    const destinationPath = join(destination, relativePath);
    mkdirSync(dirname(destinationPath), { recursive: true });
    copyFileSync(join(repositoryDirectory, relativePath), destinationPath);
  }
}
