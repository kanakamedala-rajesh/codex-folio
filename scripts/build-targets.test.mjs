import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";

const repositoryDirectory = resolve(".");
const buildScript = resolve("scripts/build.mjs");
const commandEnvironment = { ...process.env };
delete commandEnvironment.NODE_TEST_CONTEXT;
const targets = [
  { name: "linux-amd64", executable: "codex-folio" },
  { name: "windows-amd64", executable: "codex-folio.exe" },
  { name: "macos-arm64", executable: "codex-folio" },
];

for (const target of targets) {
  test(`${target.name} produces compile evidence with source identity`, () => {
    const result = spawnSync(
      process.execPath,
      [buildScript, "--build-class", "development", "--target", target.name],
      { cwd: repositoryDirectory, encoding: "utf8", env: commandEnvironment },
    );

    assert.equal(result.status, 0, result.stderr);
    const executablePath = resolve(
      "build",
      "targets",
      target.name,
      target.executable,
    );
    assert.equal(existsSync(executablePath), true);
    assertTargetBinary(target.name, readFileSync(executablePath));
    assert.match(result.stdout, new RegExp(`target: ${target.name}`));
    assert.match(result.stdout, /version: \d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?/);
    assert.match(result.stdout, /source revision: [0-9a-f]{40}/);
    assert.match(result.stdout, /build mode: (native|cross-compiled)/);
    assert.match(result.stdout, /qualification: compile-only/);
    console.log(result.stdout.trim());
  });
}

test("unsupported targets fail with an actionable target list", () => {
  const result = spawnSync(
    process.execPath,
    [buildScript, "--target", "plan9-amd64"],
    { cwd: repositoryDirectory, encoding: "utf8", env: commandEnvironment },
  );

  assert.equal(result.status, 1);
  assert.match(result.stderr, /unsupported target "plan9-amd64"/);
  assert.match(result.stderr, /linux-amd64, windows-amd64, macos-arm64/);
});

function assertTargetBinary(target, binary) {
  if (target === "linux-amd64") {
    assert.deepEqual(
      binary.subarray(0, 4),
      Buffer.from([0x7f, 0x45, 0x4c, 0x46]),
    );
    assert.equal(binary.readUInt16LE(18), 0x3e);
    return;
  }
  if (target === "windows-amd64") {
    assert.equal(binary.subarray(0, 2).toString("ascii"), "MZ");
    const peHeader = binary.readUInt32LE(0x3c);
    assert.equal(
      binary.subarray(peHeader, peHeader + 4).toString("hex"),
      "50450000",
    );
    assert.equal(binary.readUInt16LE(peHeader + 4), 0x8664);
    return;
  }
  assert.equal(binary.readUInt32LE(0), 0xfeedfacf);
  assert.equal(binary.readUInt32LE(4), 0x0100000c);
}
