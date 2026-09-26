import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { gzipSync } from "node:zlib";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const buildScript = join(rootDirectory, "scripts", "build.mjs");
const goDependencyLicensePath = join(
  rootDirectory,
  "docs",
  "development",
  "GO-DEPENDENCY-LICENSES.json",
);
const productName = "VenkataSudha CodexFolio";
const commandName = "codex-folio";
const supportedBuildClasses = new Set(["development", "prerelease", "stable"]);
const semanticVersionPattern =
  /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
const compileQualification = "compile-only; no native runtime qualification";
const stableSigningBlocker =
  "Phase 0 does not implement stable signing, attestation, or platform notarization";

export const ARCHIVE_SIZE_REVIEW_LIMIT_BYTES = 50 * 1024 * 1024;

const releaseTargets = Object.freeze([
  Object.freeze({
    name: "linux-amd64",
    goos: "linux",
    goarch: "amd64",
    executable: "codex-folio",
    installer: "install.sh",
    format: "tar.gz",
    extension: ".tar.gz",
  }),
  Object.freeze({
    name: "windows-amd64",
    goos: "windows",
    goarch: "amd64",
    executable: "codex-folio.exe",
    installer: "install.ps1",
    format: "zip",
    extension: ".zip",
  }),
  Object.freeze({
    name: "macos-arm64",
    goos: "darwin",
    goarch: "arm64",
    executable: "codex-folio",
    installer: "install.sh",
    format: "tar.gz",
    extension: ".tar.gz",
  }),
]);

const crc32Table = createCrc32Table();

function runRelease(args) {
  const options = readArguments(args);
  const version = readVersion();
  const sourceRevision = readGitValue(["rev-parse", "--verify", "HEAD"]) ?? "unknown";
  const workingTree = sourceRevision === "unknown" ? "unknown" : readDirtyState();

  if (options.buildClass === "stable") {
    const blockers = stableArtifactBlockers(version, sourceRevision, workingTree);
    if (blockers.length > 0) {
      throw new Error(`stable artifact request rejected:\n- ${blockers.join("\n- ")}`);
    }
  }

  const outputDirectory = resolve(
    rootDirectory,
    options.outputDirectory ?? join("build", "releases", `${version}-${options.buildClass}`),
  );
  mkdirSync(outputDirectory, { recursive: true });

  const archives = releaseTargets.map((target) =>
    createTargetArchive({
      target,
      version,
      buildClass: options.buildClass,
      sourceRevision,
      workingTree,
      outputDirectory,
    }),
  );

  const inventory = createDependencyLicenseInventory(version, options.buildClass);
  const sbom = createSbom(inventory, version, options.buildClass);
  const provenance = createProvenance(archives, version, options.buildClass, sourceRevision);
  const signingStatus = createSigningStatus(options.buildClass);

  writeJSON(join(outputDirectory, "dependency-licenses.json"), inventory);
  writeJSON(join(outputDirectory, "sbom.cdx.json"), sbom);
  writeJSON(join(outputDirectory, "provenance.json"), provenance);
  writeJSON(join(outputDirectory, "signing-status.json"), signingStatus);
  writeFileSync(
    join(outputDirectory, "SHA256SUMS"),
    `${archives.map((archive) => `${archive.sha256}  ${archive.archive}`).join("\n")}\n`,
  );

  const manifest = createReleaseManifest({
    version,
    buildClass: options.buildClass,
    sourceRevision,
    workingTree,
    archives,
  });
  writeJSON(join(outputDirectory, "release-manifest.json"), manifest);

  console.log(`release dry-run: ${options.buildClass}`);
  console.log(`output directory: ${relative(rootDirectory, outputDirectory) || "."}`);
  for (const archive of archives) {
    console.log(
      `archive: ${archive.archive} (${archive.size_bytes} bytes; size review: ${archive.size_review})`,
    );
    if (archive.size_review === "review-required") {
      console.warn(`review required: ${archive.archive} exceeds the 50 MB archive threshold`);
    }
  }
  console.log("supply-chain metadata: dependency licenses, SBOM, provenance placeholder, signing status");
  console.log("stable eligibility: no; development/prerelease archive only");
  console.log("remote mutation: none");

  return manifest;
}

export function classifyArchiveSize(sizeBytes) {
  return sizeBytes > ARCHIVE_SIZE_REVIEW_LIMIT_BYTES ? "review-required" : "pass";
}

function readArguments(args) {
  let dryRun = false;
  let buildClass = "development";
  let outputDirectory = null;

  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--dry-run") {
      dryRun = true;
      continue;
    }
    if (argument === "--build-class") {
      buildClass = args[index + 1] ?? "";
      index += 1;
      continue;
    }
    if (argument === "--output-dir") {
      outputDirectory = args[index + 1] ?? "";
      index += 1;
      continue;
    }
    throw new Error(
      `unexpected argument ${JSON.stringify(argument)}; use --dry-run [--build-class <class>] [--output-dir <directory>]`,
    );
  }

  if (!dryRun) {
    throw new Error("release only supports the local --dry-run mode in Phase 0");
  }
  if (!supportedBuildClasses.has(buildClass)) {
    throw new Error(
      `unsupported build class ${JSON.stringify(buildClass)}; choose development, prerelease, or stable`,
    );
  }
  if (outputDirectory === "") {
    throw new Error("--output-dir requires a directory");
  }

  return { buildClass, outputDirectory };
}

function stableArtifactBlockers(version, sourceRevision, workingTree) {
  const blockers = [];
  if (version.includes("-")) {
    blockers.push(`product version ${version} is a prerelease version`);
  }
  if (sourceRevision === "unknown") {
    blockers.push("source revision is unknown");
  }
  if (workingTree !== "clean") {
    blockers.push(`working tree is ${workingTree}`);
  }
  blockers.push(stableSigningBlocker);
  return blockers;
}

function createTargetArchive({
  target,
  version,
  buildClass,
  sourceRevision,
  workingTree,
  outputDirectory,
}) {
  runTargetBuild(target, buildClass);
  const executablePath = join(
    rootDirectory,
    "build",
    "targets",
    target.name,
    target.executable,
  );
  if (!existsSync(executablePath)) {
    throw new Error(`target build did not produce ${executablePath}`);
  }

  const archive = archiveName(version, buildClass, target);
  const stem = archive.replace(/\.(?:tar\.gz|zip)$/, "");
  const buildInfo = createBuildInfo({
    archive,
    stem,
    target,
    version,
    buildClass,
    sourceRevision,
    workingTree,
  });
  const entries = [
    {
      name: `${stem}/${target.executable}`,
      data: readFileSync(executablePath),
      mode: 0o755,
    },
    {
      name: `${stem}/${target.installer}`,
      data: Buffer.from(createInstaller(target, version, buildClass), "utf8"),
      mode: target.format === "zip" ? 0o644 : 0o755,
    },
    {
      name: `${stem}/BUILD-INFO.json`,
      data: Buffer.from(`${JSON.stringify(buildInfo, null, 2)}\n`, "utf8"),
      mode: 0o644,
    },
    {
      name: `${stem}/INSTALL.md`,
      data: Buffer.from(createInstallGuide(target, version, buildClass), "utf8"),
      mode: 0o644,
    },
    {
      name: `${stem}/LICENSE`,
      data: readFileSync(join(rootDirectory, "LICENSE")),
      mode: 0o644,
    },
    {
      name: `${stem}/NOTICE`,
      data: readFileSync(join(rootDirectory, "NOTICE")),
      mode: 0o644,
    },
  ];
  if (target.goos === "linux") {
    const helperPath = join(
      rootDirectory,
      "build",
      "targets",
      target.name,
      "codex-folio-wsl-vault.exe",
    );
    if (!existsSync(helperPath)) {
      throw new Error(`target build did not produce ${helperPath}`);
    }
    entries.push({
      name: `${stem}/codex-folio-wsl-vault.exe`,
      data: readFileSync(helperPath),
      mode: 0o755,
    });
  }
  const archiveData = target.format === "zip" ? createZip(entries) : createTarGz(entries);
  writeFileSync(join(outputDirectory, archive), archiveData);

  return {
    target: target.name,
    archive,
    format: target.format,
    sha256: sha256(archiveData),
    size_bytes: archiveData.length,
    size_review: classifyArchiveSize(archiveData.length),
    build_identity: `${stem}/BUILD-INFO.json`,
  };
}

function runTargetBuild(target, buildClass) {
  const result = spawnSync(
    process.execPath,
    [buildScript, "--build-class", buildClass, "--target", target.name],
    {
      cwd: rootDirectory,
      env: {
        ...process.env,
        GOCACHE: process.env.GOCACHE ?? join(tmpdir(), "codex-folio-go-cache"),
      },
      stdio: "inherit",
    },
  );
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`${target.name} build failed with status ${result.status ?? "unknown"}`);
  }
}

function createBuildInfo({
  archive,
  stem,
  target,
  version,
  buildClass,
  sourceRevision,
  workingTree,
}) {
  return {
    schema: "codex-folio.build-identity.v1",
    product: productName,
    command: commandName,
    version,
    build_classification: buildClass,
    source_revision: sourceRevision,
    working_tree: workingTree,
    target: target.name,
    goos: target.goos,
    goarch: target.goarch,
    build_mode: targetIsNative(target) ? "native" : "cross-compiled",
    qualification: compileQualification,
    archive: archive,
    archive_root: stem,
    archive_format: target.format,
    stable_eligible: false,
  };
}

function createInstallGuide(target, version, buildClass) {
  const command = target.format === "zip" ? ".\\install.ps1" : "./install.sh";
  const payload =
    target.goos === "linux"
      ? "the executable and bundled Windows-backed WSL vault helper"
      : "the executable";
  return `# VenkataSudha CodexFolio ${version}\n\nThis ${buildClass} archive is an unsigned Phase 0 dry-run artifact for ${target.name}.\nIt contains compile-only evidence and is not a stable release or native runtime qualification.\n\nInspect the installer before running it. The installer copies ${payload} to a user-local directory; it does not run during the release dry run.\n\nInstaller helper: \`${command}\`\n`;
}

function createInstaller(target, version, buildClass) {
  if (target.format === "zip") {
    return `$ErrorActionPreference = "Stop"\n\n# VenkataSudha CodexFolio ${version} ${buildClass} ${target.name}\n$installDirectory = if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {\n  Join-Path $HOME "AppData\\Local\\Programs\\CodexFolio"\n} else {\n  Join-Path $env:LOCALAPPDATA "Programs\\CodexFolio"\n}\n\nNew-Item -ItemType Directory -Force -Path $installDirectory | Out-Null\nCopy-Item -Force (Join-Path $PSScriptRoot "codex-folio.exe") (Join-Path $installDirectory "codex-folio.exe")\nWrite-Output "Installed codex-folio.exe to $installDirectory"\n`;
  }

  const helperInstall =
    target.goos === "linux"
      ? `install -m 0755 "$script_directory/codex-folio-wsl-vault.exe" "$install_directory/codex-folio-wsl-vault.exe"\n`
      : "";
  return `#!/bin/sh\nset -eu\n\n# VenkataSudha CodexFolio ${version} ${buildClass} ${target.name}\ninstall_directory="\${PREFIX:-$HOME/.local}/bin"\nscript_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)\nmkdir -p "$install_directory"\ninstall -m 0755 "$script_directory/codex-folio" "$install_directory/codex-folio"\n${helperInstall}printf 'Installed codex-folio to %s\\n' "$install_directory/codex-folio"\n`;
}

function createDependencyLicenseInventory(version, buildClass) {
  const lockfilePath = join(rootDirectory, "web", "package-lock.json");
  const lockfile = JSON.parse(readFileSync(lockfilePath, "utf8"));
  const npmDependencies = Object.entries(lockfile.packages ?? {})
    .filter(([packagePath]) => packagePath !== "")
    .map(([packagePath, packageData]) => {
      const license = normalizeLicense(packageData.license);
      if (!license) {
        throw new Error(`dependency ${packagePath} has no license metadata in web/package-lock.json`);
      }
      return {
        package_path: packagePath,
        name: packageName(packagePath),
        version: packageData.version,
        license,
        scope: packageData.dev === true ? "development" : "runtime",
      };
    })
    .sort(compareDependency);
  const goModule = parseGoMod(readFileSync(join(rootDirectory, "go.mod"), "utf8"));
  const goLicenseReviews = readJSON(goDependencyLicensePath).dependencies ?? [];
  const goDependencies = goModule.dependencies.map((dependency) => {
    const review = goLicenseReviews.find(
      (candidate) =>
        candidate.path === dependency.path &&
        candidate.version === dependency.version,
    );
    if (!review || !normalizeLicense(review.license)) {
      throw new Error(
        `Go dependency ${dependency.path}@${dependency.version} has no reviewed license metadata`,
      );
    }
    return {
      ...dependency,
      license: normalizeLicense(review.license),
      scope: dependency.indirect ? "runtime-transitive" : "runtime",
    };
  });
  const licenseSummary = countLicenses([...goDependencies, ...npmDependencies]);

  return {
    schema: "codex-folio.dependency-licenses.v1",
    product: productName,
    version,
    build_classification: buildClass,
    generated_from: ["go.mod", "web/package-lock.json"],
    sources: [
      {
        ecosystem: "go",
        file: "go.mod",
        module: goModule.module,
        dependencies: goDependencies,
      },
      {
        ecosystem: "npm",
        file: "web/package-lock.json",
        lockfile_version: lockfile.lockfileVersion,
        dependencies: npmDependencies,
      },
    ],
    license_summary: licenseSummary,
  };
}

function createSbom(inventory, version, buildClass) {
  const components = inventory.sources.flatMap((source) =>
    source.dependencies.map((dependency) => {
      const identifier =
        source.ecosystem === "npm"
          ? dependency.package_path
          : `${dependency.path}@${dependency.version}`;
      const properties =
        source.ecosystem === "npm"
          ? [{ name: "codex-folio:lock-path", value: dependency.package_path }]
          : [{ name: "codex-folio:module-path", value: dependency.path }];
      return {
        type: "library",
        "bom-ref": `urn:codex-folio:${source.ecosystem}:${sha256(identifier).slice(0, 24)}`,
        name: source.ecosystem === "npm" ? dependency.name : dependency.path,
        version: dependency.version,
        scope: dependency.scope === "runtime" ? "required" : "optional",
        licenses: [{ license: { name: dependency.license } }],
        properties,
      };
    }),
  );
  const inventoryDigest = sha256(Buffer.from(JSON.stringify(inventory), "utf8"));

  return {
    bomFormat: "CycloneDX",
    specVersion: "1.5",
    serialNumber: `urn:uuid:${inventoryDigest.slice(0, 8)}-${inventoryDigest.slice(8, 12)}-${inventoryDigest.slice(12, 16)}-${inventoryDigest.slice(16, 20)}-${inventoryDigest.slice(20, 32)}`,
    version: 1,
    metadata: {
      component: {
        type: "application",
        name: commandName,
        version,
      },
      properties: [
        { name: "codex-folio:build-classification", value: buildClass },
        { name: "codex-folio:stable-eligible", value: "false" },
        { name: "codex-folio:qualification", value: "development-only" },
      ],
    },
    components,
  };
}

function createProvenance(archives, version, buildClass, sourceRevision) {
  return {
    schema: "codex-folio.provenance.v1",
    status: "placeholder",
    placeholder: true,
    qualification: "development-only; no signed provenance",
    stable_eligible: false,
    reason: "Phase 0 dry runs do not create attestations, sign artifacts, or publish to a provenance service",
    product: productName,
    version,
    build_classification: buildClass,
    source_revision: sourceRevision,
    subjects: archives.map((archive) => ({
      name: archive.archive,
      sha256: archive.sha256,
    })),
    inputs: [
      inputDigest("go.mod"),
      inputDigest("docs/development/GO-DEPENDENCY-LICENSES.json"),
      inputDigest("web/package-lock.json"),
      inputDigest("internal/buildinfo/version.txt"),
    ],
  };
}

function createSigningStatus(buildClass) {
  return {
    schema: "codex-folio.signing-status.v1",
    build_classification: buildClass,
    stable_eligible: false,
    credentials_used: false,
    remote_mutation: false,
    stages: [
      {
        name: "sigstore-signing",
        status: "skipped",
        reason: "Phase 0 dry run has no signing identity or OIDC credential",
      },
      {
        name: "sigstore-attestation",
        status: "skipped",
        reason: "Phase 0 dry run does not publish attestations",
      },
      {
        name: "windows-authenticode",
        target: "windows-amd64",
        status: "skipped",
        reason: "Windows certificate credentials are unavailable in the secret-free dry run",
      },
      {
        name: "macos-developer-id",
        target: "macos-arm64",
        status: "skipped",
        reason: "Developer ID credentials are unavailable in the secret-free dry run",
      },
      {
        name: "macos-notarization",
        target: "macos-arm64",
        status: "skipped",
        reason: "Notarization credentials and service access are unavailable in the secret-free dry run",
      },
    ],
    stable_gate: {
      status: "not-satisfied",
      reason: stableSigningBlocker,
    },
  };
}

function createReleaseManifest({
  version,
  buildClass,
  sourceRevision,
  workingTree,
  archives,
}) {
  return {
    schema: "codex-folio.release-manifest.v1",
    product: productName,
    command: commandName,
    version,
    build_classification: buildClass,
    release_status: `unsigned-${buildClass}-dry-run`,
    dry_run: true,
    remote_mutation: false,
    stable_eligible: false,
    source_revision: sourceRevision,
    working_tree: workingTree,
    qualification: compileQualification,
    archive_size_review_threshold_bytes: ARCHIVE_SIZE_REVIEW_LIMIT_BYTES,
    archives,
    metadata: {
      checksums: "SHA256SUMS",
      dependency_licenses: "dependency-licenses.json",
      sbom: "sbom.cdx.json",
      provenance: "provenance.json",
      signing_status: "signing-status.json",
    },
  };
}

function createArchiveEntries(entries) {
  return [...entries].sort((left, right) => compareText(left.name, right.name));
}

function createTarGz(entries) {
  const sortedEntries = createArchiveEntries(entries);
  const blocks = [];
  for (const entry of sortedEntries) {
    const data = Buffer.isBuffer(entry.data) ? entry.data : Buffer.from(entry.data);
    blocks.push(createTarHeader(entry.name, data.length, entry.mode));
    blocks.push(data);
    const padding = (512 - (data.length % 512)) % 512;
    if (padding > 0) blocks.push(Buffer.alloc(padding));
  }
  blocks.push(Buffer.alloc(1024));
  const compressed = gzipSync(Buffer.concat(blocks), { level: 9, mtime: 0 });
  compressed[9] = 3;
  return compressed;
}

function createTarHeader(name, size, mode) {
  const header = Buffer.alloc(512);
  writeTarString(header, 0, 100, name);
  writeTarOctal(header, 100, 8, mode);
  writeTarOctal(header, 108, 8, 0);
  writeTarOctal(header, 116, 8, 0);
  writeTarOctal(header, 124, 12, size);
  writeTarOctal(header, 136, 12, 0);
  header.fill(0x20, 148, 156);
  header[156] = 0x30;
  writeTarString(header, 257, 6, "ustar\0");
  writeTarString(header, 263, 2, "00");

  const checksum = header.reduce((total, byte) => total + byte, 0);
  const checksumText = checksum.toString(8).padStart(6, "0");
  writeTarString(header, 148, 6, checksumText);
  header[154] = 0;
  header[155] = 0x20;
  return header;
}

function writeTarString(buffer, offset, length, value) {
  const encoded = Buffer.from(value, "utf8");
  if (encoded.length > length) {
    throw new Error(`tar field ${JSON.stringify(value)} exceeds ${length} bytes`);
  }
  encoded.copy(buffer, offset);
}

function writeTarOctal(buffer, offset, length, value) {
  const encoded = Number(value).toString(8).padStart(length - 1, "0");
  if (encoded.length > length - 1) {
    throw new Error(`tar numeric field ${value} exceeds ${length} bytes`);
  }
  buffer.write(encoded, offset, length - 1, "ascii");
}

function createZip(entries) {
  const sortedEntries = createArchiveEntries(entries);
  const localRecords = [];
  const centralRecords = [];
  let offset = 0;

  for (const entry of sortedEntries) {
    const data = Buffer.isBuffer(entry.data) ? entry.data : Buffer.from(entry.data);
    const name = Buffer.from(entry.name, "utf8");
    const checksum = crc32(data);
    const localHeader = Buffer.alloc(30);
    localHeader.writeUInt32LE(0x04034b50, 0);
    localHeader.writeUInt16LE(20, 4);
    localHeader.writeUInt16LE(0x0800, 6);
    localHeader.writeUInt16LE(0, 8);
    localHeader.writeUInt16LE(0, 10);
    localHeader.writeUInt16LE(33, 12);
    localHeader.writeUInt32LE(checksum, 14);
    localHeader.writeUInt32LE(data.length, 18);
    localHeader.writeUInt32LE(data.length, 22);
    localHeader.writeUInt16LE(name.length, 26);
    localHeader.writeUInt16LE(0, 28);
    localRecords.push(Buffer.concat([localHeader, name, data]));

    const centralHeader = Buffer.alloc(46);
    centralHeader.writeUInt32LE(0x02014b50, 0);
    centralHeader.writeUInt16LE(0x0314, 4);
    centralHeader.writeUInt16LE(20, 6);
    centralHeader.writeUInt16LE(0x0800, 8);
    centralHeader.writeUInt16LE(0, 10);
    centralHeader.writeUInt16LE(0, 12);
    centralHeader.writeUInt16LE(33, 14);
    centralHeader.writeUInt32LE(checksum, 16);
    centralHeader.writeUInt32LE(data.length, 20);
    centralHeader.writeUInt32LE(data.length, 24);
    centralHeader.writeUInt16LE(name.length, 28);
    centralHeader.writeUInt16LE(0, 30);
    centralHeader.writeUInt16LE(0, 32);
    centralHeader.writeUInt16LE(0, 34);
    centralHeader.writeUInt16LE(0, 36);
    centralHeader.writeUInt32LE((entry.mode & 0o777) << 16, 38);
    centralHeader.writeUInt32LE(offset, 42);
    centralRecords.push(Buffer.concat([centralHeader, name]));
    offset += localRecords.at(-1).length;
  }

  const centralDirectory = Buffer.concat(centralRecords);
  const localData = Buffer.concat(localRecords);
  const endRecord = Buffer.alloc(22);
  endRecord.writeUInt32LE(0x06054b50, 0);
  endRecord.writeUInt16LE(0, 4);
  endRecord.writeUInt16LE(0, 6);
  endRecord.writeUInt16LE(sortedEntries.length, 8);
  endRecord.writeUInt16LE(sortedEntries.length, 10);
  endRecord.writeUInt32LE(centralDirectory.length, 12);
  endRecord.writeUInt32LE(localData.length, 16);
  endRecord.writeUInt16LE(0, 20);
  return Buffer.concat([localData, centralDirectory, endRecord]);
}

function createCrc32Table() {
  const table = new Uint32Array(256);
  for (let index = 0; index < table.length; index += 1) {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) {
      value = (value & 1) === 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    }
    table[index] = value >>> 0;
  }
  return table;
}

function crc32(data) {
  let value = 0xffffffff;
  for (const byte of data) {
    value = crc32Table[(value ^ byte) & 0xff] ^ (value >>> 8);
  }
  return (value ^ 0xffffffff) >>> 0;
}

function archiveName(version, buildClass, target) {
  const classSuffix = buildClass === "stable" ? "" : `-${buildClass}`;
  return `${commandName}-${version}${classSuffix}-${target.name}${target.extension}`;
}

function targetIsNative(target) {
  const nativePlatform = process.platform === "win32" ? "windows" : process.platform;
  const nativeArchitecture = process.arch === "x64" ? "amd64" : process.arch;
  return target.goos === nativePlatform && target.goarch === nativeArchitecture;
}

function readVersion() {
  const version = readFileSync(
    join(rootDirectory, "internal", "buildinfo", "version.txt"),
    "utf8",
  ).trim();
  if (!semanticVersionPattern.test(version)) {
    throw new Error(`invalid product version ${JSON.stringify(version)}`);
  }
  return version;
}

function readDirtyState() {
  const status = readGitValue(["status", "--porcelain", "--untracked-files=all"]);
  return status === null ? "unknown" : status.length === 0 ? "clean" : "dirty";
}

function readGitValue(args) {
  const result = spawnSync("git", args, {
    cwd: rootDirectory,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  });
  if (result.error || result.status !== 0) return null;
  return result.stdout.trim();
}

function packageName(packagePath) {
  const marker = "node_modules/";
  const markerIndex = packagePath.lastIndexOf(marker);
  return markerIndex === -1 ? packagePath : packagePath.slice(markerIndex + marker.length);
}

function normalizeLicense(value) {
  if (typeof value === "string" && value.trim() !== "") return value.trim();
  if (value && typeof value === "object") {
    if (typeof value.id === "string" && value.id.trim() !== "") return value.id.trim();
    if (typeof value.name === "string" && value.name.trim() !== "") return value.name.trim();
  }
  if (Array.isArray(value)) {
    const licenses = value.map(normalizeLicense).filter(Boolean).sort();
    return licenses.length === 0 ? null : licenses.join(" AND ");
  }
  return null;
}

function compareDependency(left, right) {
  return compareText(
    `${left.name}@${left.version}@${left.package_path}`,
    `${right.name}@${right.version}@${right.package_path}`,
  );
}

function countLicenses(dependencies) {
  const counts = new Map();
  for (const dependency of dependencies) {
    counts.set(dependency.license, (counts.get(dependency.license) ?? 0) + 1);
  }
  return Object.fromEntries(
    [...counts.entries()].sort(([left], [right]) => compareText(left, right)),
  );
}

function parseGoMod(contents) {
  const lines = contents.split(/\r?\n/);
  const module = lines.find((line) => line.trim().startsWith("module "))?.trim().slice(7) ?? "unknown";
  const dependencies = [];
  let inRequireBlock = false;
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed === "require (") {
      inRequireBlock = true;
      continue;
    }
    if (inRequireBlock && trimmed === ")") {
      inRequireBlock = false;
      continue;
    }
    if (!inRequireBlock && !trimmed.startsWith("require ")) continue;
    const declaration = trimmed.replace(/^require\s+/, "").replace(/\s+\/\/.*$/, "");
    const [path, version] = declaration.split(/\s+/);
    if (path && version && path !== "(") {
      dependencies.push({ path, version, indirect: trimmed.includes("// indirect") });
    }
  }
  dependencies.sort((left, right) =>
    compareText(`${left.path}@${left.version}`, `${right.path}@${right.version}`),
  );
  return { module, dependencies };
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function inputDigest(path) {
  return { path, sha256: sha256(readFileSync(join(rootDirectory, path))) };
}

function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

function writeJSON(path, value) {
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

const isMainModule = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isMainModule) {
  try {
    runRelease(process.argv.slice(2));
  } catch (error) {
    console.error(`release failed: ${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  }
}
