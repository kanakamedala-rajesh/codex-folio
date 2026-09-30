import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
} from "node:fs";
import { join, relative, resolve } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";

import {
  ARCHIVE_SIZE_REVIEW_LIMIT_BYTES,
  classifyArchiveSize,
} from "./release.mjs";

const repositoryDirectory = resolve(".");
const releaseScript = resolve("scripts/release.mjs");
const commandEnvironment = { ...process.env };
delete commandEnvironment.NODE_TEST_CONTEXT;

test("development dry run creates all deterministic tier-one archives and metadata", () => {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), "codex-folio-release-test-"));
  const firstOutput = join(temporaryDirectory, "first");
  const secondOutput = join(temporaryDirectory, "second");

  try {
    const firstResult = runRelease(firstOutput);
    assert.equal(firstResult.status, 0, firstResult.stderr);
    assert.match(firstResult.stdout, /release dry-run: development/);
    assert.match(firstResult.stdout, /remote mutation: none/);

    const secondResult = runRelease(secondOutput);
    assert.equal(secondResult.status, 0, secondResult.stderr);

    const firstManifest = readJSON(join(firstOutput, "release-manifest.json"));
    const secondManifest = readJSON(join(secondOutput, "release-manifest.json"));
    assert.deepEqual(firstManifest, secondManifest);
    assert.equal(firstManifest.dry_run, true);
    assert.equal(firstManifest.remote_mutation, false);
    assert.equal(firstManifest.stable_eligible, false);
    assert.equal(
      firstManifest.archive_size_review_threshold_bytes,
      ARCHIVE_SIZE_REVIEW_LIMIT_BYTES,
    );
    assert.deepEqual(
      firstManifest.archives.map((archive) => archive.target),
      ["linux-amd64", "windows-amd64", "macos-arm64"],
    );

    const firstFiles = readdirSync(firstOutput).sort();
    const secondFiles = readdirSync(secondOutput).sort();
    assert.deepEqual(firstFiles, secondFiles);
    for (const file of firstFiles) {
      assert.deepEqual(
        readFileSync(join(firstOutput, file)),
        readFileSync(join(secondOutput, file)),
        `release output ${file} was not deterministic`,
      );
    }

    const checksums = readFileSync(join(firstOutput, "SHA256SUMS"), "utf8")
      .trim()
      .split("\n")
      .map((line) => line.split(/ {2}/));
    assert.equal(checksums.length, 3);
    for (const [expectedDigest, archiveName] of checksums) {
      const actualDigest = createHash("sha256")
        .update(readFileSync(join(firstOutput, archiveName)))
        .digest("hex");
      assert.equal(actualDigest, expectedDigest);
    }

    for (const archive of firstManifest.archives) {
      const archivePath = join(firstOutput, archive.archive);
      assert.equal(existsSync(archivePath), true);
      assert.equal(archive.sha256, sha256(readFileSync(archivePath)));
      assert.equal(archive.size_review, "pass");

      const stem = archive.archive.replace(/\.(?:tar\.gz|zip)$/, "");
      const executable = archive.target === "windows-amd64" ? "codex-folio.exe" : "codex-folio";
      const installer = archive.target === "windows-amd64" ? "install.ps1" : "install.sh";
      const entries = listArchiveEntries(archivePath, archive.format);
      for (const member of [
        `${stem}/${executable}`,
        `${stem}/${installer}`,
        `${stem}/BUILD-INFO.json`,
        `${stem}/INSTALL.md`,
        `${stem}/LICENSE`,
        `${stem}/NOTICE`,
      ]) {
        assert.equal(entries.includes(member), true, `${member} missing from ${archive.archive}`);
      }
      if (archive.target === "linux-amd64") {
        assert.equal(entries.includes(`${stem}/codex-folio-wsl-vault.exe`), true);
        assert.match(
          readArchiveMember(archivePath, archive.format, `${stem}/install.sh`),
          /codex-folio-wsl-vault\.exe/,
        );
      }

      const buildInfo = readArchiveMember(archivePath, archive.format, `${stem}/BUILD-INFO.json`);
      const parsedBuildInfo = JSON.parse(buildInfo);
      assert.equal(parsedBuildInfo.target, archive.target);
      assert.equal(parsedBuildInfo.build_classification, "development");
      assert.equal(parsedBuildInfo.qualification, "compile-only; no native runtime qualification");
    }

    const inventory = readJSON(join(firstOutput, "dependency-licenses.json"));
    assert.equal(inventory.sources[0].dependencies.length > 0, true);
    assert.equal(inventory.sources[0].dependencies.every((dependency) => dependency.license), true);
    assert.equal(
      inventory.sources[0].dependencies.some(
        (dependency) => dependency.path === "modernc.org/sqlite" && dependency.version === "v1.57.0",
      ),
      true,
    );
    assert.equal(inventory.sources[1].lockfile_version, 3);
    assert.equal(inventory.sources[1].dependencies.length > 0, true);
    assert.equal(inventory.sources[1].dependencies.every((dependency) => dependency.license), true);
    const dependencyKeys = inventory.sources[1].dependencies.map(
      (dependency) => `${dependency.name}@${dependency.version}@${dependency.package_path}`,
    );
    assert.deepEqual(dependencyKeys, [...dependencyKeys].sort());

    const sbom = readJSON(join(firstOutput, "sbom.cdx.json"));
    assert.equal(sbom.bomFormat, "CycloneDX");
    assert.equal(sbom.specVersion, "1.5");
    assert.equal(
      sbom.components.length,
      inventory.sources[0].dependencies.length + inventory.sources[1].dependencies.length,
    );
    assert.equal(
      sbom.metadata.properties.some(
        (property) => property.name === "codex-folio:stable-eligible" && property.value === "false",
      ),
      true,
    );

    const provenance = readJSON(join(firstOutput, "provenance.json"));
    assert.equal(provenance.placeholder, true);
    assert.equal(provenance.stable_eligible, false);
    assert.equal(provenance.subjects.length, 3);

    const signingStatus = readJSON(join(firstOutput, "signing-status.json"));
    assert.equal(signingStatus.stable_eligible, false);
    assert.equal(signingStatus.stages.every((stage) => stage.status === "skipped"), true);
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
});

test("stable archive requests fail closed before producing output", () => {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), "codex-folio-stable-test-"));
  const outputDirectory = join(temporaryDirectory, "stable");

  try {
    const result = spawnSync(
      process.execPath,
      [releaseScript, "--dry-run", "--build-class", "stable", "--output-dir", outputDirectory],
      { cwd: repositoryDirectory, encoding: "utf8", env: commandEnvironment },
    );

    assert.equal(result.status, 1);
    assert.match(result.stderr, /stable artifact request rejected/);
    assert.equal(existsSync(outputDirectory), false);
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
});

test("prerelease archives carry a visible classification in names and guidance", () => {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), "codex-folio-prerelease-test-"));
  const outputDirectory = join(temporaryDirectory, "prerelease");

  try {
    const result = runRelease(outputDirectory, "prerelease");
    assert.equal(result.status, 0, result.stderr);
    const manifest = readJSON(join(outputDirectory, "release-manifest.json"));
    assert.equal(manifest.release_status, "unsigned-prerelease-dry-run");
    assert.equal(
      manifest.archives.every((archive) => archive.archive.includes("-prerelease-")),
      true,
    );

    const archive = manifest.archives[0];
    const archivePath = join(outputDirectory, archive.archive);
    const stem = archive.archive.replace(/\.(?:tar\.gz|zip)$/, "");
    assert.match(
      readArchiveMember(archivePath, archive.format, `${stem}/INSTALL.md`),
      /prerelease archive is an unsigned Phase 0 dry-run artifact/,
    );
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
});

test("archives above the review threshold are explicitly flagged", () => {
  assert.equal(classifyArchiveSize(ARCHIVE_SIZE_REVIEW_LIMIT_BYTES), "pass");
  assert.equal(classifyArchiveSize(ARCHIVE_SIZE_REVIEW_LIMIT_BYTES + 1), "review-required");
});

test("archive entry paths use portable separators", () => {
  assert.equal(archiveEntryPath("release\\codex-folio"), "release/codex-folio");
  assert.equal(archiveEntryPath("release/codex-folio"), "release/codex-folio");
  assert.equal(archiveEntryPath("release/codex-folio\r"), "release/codex-folio");
});

function runRelease(outputDirectory, buildClass = "development") {
  return spawnSync(
    process.execPath,
    [releaseScript, "--dry-run", "--build-class", buildClass, "--output-dir", outputDirectory],
    { cwd: repositoryDirectory, encoding: "utf8", env: commandEnvironment },
  );
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function sha256(buffer) {
  return createHash("sha256").update(buffer).digest("hex");
}

function listArchiveEntries(path, format) {
  if (format === "zip" && process.platform === "win32") {
    const extractedDirectory = extractZipOnWindows(path);
    try {
      return collectFiles(extractedDirectory)
        .map((file) => archiveEntryPath(relative(extractedDirectory, file)))
        .sort();
    } finally {
      rmSync(extractedDirectory, { recursive: true, force: true });
    }
  }
  const result =
    format === "zip"
      ? spawnSync("unzip", ["-Z1", path], { encoding: "utf8" })
      : spawnSync("tar", ["-tzf", path], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout
    .trim()
    .split("\n")
    .filter(Boolean)
    .map(archiveEntryPath);
}

function archiveEntryPath(path) {
  return path.replaceAll("\\", "/").replace(/\r$/, "");
}

function readArchiveMember(path, format, member) {
  if (format === "zip" && process.platform === "win32") {
    const extractedDirectory = extractZipOnWindows(path);
    try {
      return readFileSync(
        join(extractedDirectory, ...member.split("/")),
        "utf8",
      );
    } finally {
      rmSync(extractedDirectory, { recursive: true, force: true });
    }
  }
  const result =
    format === "zip"
      ? spawnSync("unzip", ["-p", path, member], { encoding: "utf8" })
      : spawnSync("tar", ["-xOzf", path, member], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
}

function extractZipOnWindows(path) {
  const extractedDirectory = mkdtempSync(
    join(tmpdir(), "codex-folio-zip-test-"),
  );
  const result = spawnSync(
    "powershell.exe",
    [
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      "Expand-Archive -LiteralPath $env:CODEX_FOLIO_ARCHIVE_PATH -DestinationPath $env:CODEX_FOLIO_EXTRACT_PATH -Force",
    ],
    {
      encoding: "utf8",
      env: {
        ...commandEnvironment,
        CODEX_FOLIO_ARCHIVE_PATH: path,
        CODEX_FOLIO_EXTRACT_PATH: extractedDirectory,
      },
    },
  );
  assert.equal(result.status, 0, result.stderr);
  return extractedDirectory;
}

function collectFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? collectFiles(path) : [path];
  });
}
