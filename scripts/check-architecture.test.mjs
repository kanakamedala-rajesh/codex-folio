import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { architectureFailures } from "./check-architecture.mjs";

test("feature modules may use concrete domain code without ports", () => {
  const root = fixture({
    "internal/profile/profile.go": `package profile\n\ntype Profile struct { Alias string }\n`,
  });

  assert.deepEqual(architectureFailures(root), []);
});

test("feature modules cannot import outward adapters", () => {
  const root = fixture({
    "internal/profile/profile.go": `package profile\n\nimport "example.test/codex-folio/internal/store"\n\nvar _ = store.Value\n`,
    "internal/store/store.go": `package store\n\nvar Value = true\n`,
  });

  assert.deepEqual(architectureFailures(root), [
    "internal/profile: feature module must not import outward adapter internal/store",
  ]);
});

test("outward adapters cannot depend on other outward adapters", () => {
  const root = fixture({
    "internal/httpapi/handler.go": `package httpapi\n\nimport "example.test/codex-folio/internal/store"\n\nvar _ = store.Value\n`,
    "internal/store/store.go": `package store\n\nvar Value = true\n`,
  });

  assert.deepEqual(architectureFailures(root), [
    "internal/httpapi: outward adapter must not import outward adapter internal/store",
  ]);
});

test("nested adapter packages remain on the outward side", () => {
  const root = fixture({
    "internal/platform/windows/service.go": `package windows\n\nimport "example.test/codex-folio/internal/store/sqlite"\n\nvar _ = sqlite.Value\n`,
    "internal/store/sqlite/sqlite.go": `package sqlite\n\nvar Value = true\n`,
  });

  assert.deepEqual(architectureFailures(root), [
    "internal/platform/windows: outward adapter must not import outward adapter internal/store/sqlite",
  ]);
});

test("new internal package roots must be classified", () => {
  const root = fixture({
    "internal/codexadapter/client.go": `package codexadapter\n`,
  });

  assert.deepEqual(architectureFailures(root), [
    "internal/codexadapter: internal package root must be classified as a feature, adapter, or support package in scripts/check-architecture.mjs",
  ]);
});

test("comments that resemble imports are ignored by the Go package graph", () => {
  const root = fixture({
    "internal/profile/profile.go": `package profile\n\n// import "example.test/codex-folio/internal/store"\ntype Profile struct{}\n`,
  });

  assert.deepEqual(architectureFailures(root), []);
});

test("build-constrained imports are checked outside the host build context", () => {
  const root = fixture({
    "internal/profile/profile_special.go": `//go:build codexfolio_special\n\npackage profile\n\nimport "example.test/codex-folio/internal/platform/windows"\n\nvar _ = windows.Value\n`,
    "internal/platform/windows/windows.go": `package windows\n\nvar Value = true\n`,
  });

  assert.deepEqual(architectureFailures(root), [
    "internal/profile: feature module must not import outward adapter internal/platform/windows",
  ]);
});

function fixture(files) {
  const root = mkdtempSync(join(tmpdir(), "codex-folio-architecture-"));
  writeFile(root, "go.mod", "module example.test/codex-folio\n\ngo 1.27.0\n");
  writeFile(
    root,
    "cmd/codex-folio/main.go",
    "package main\n\nfunc main() {}\n",
  );
  for (const [name, content] of Object.entries(files)) {
    writeFile(root, name, content);
  }
  return root;
}

function writeFile(root, name, content) {
  const path = join(root, name);
  mkdirSync(join(path, ".."), { recursive: true });
  writeFileSync(path, content);
}
