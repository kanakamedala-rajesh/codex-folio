import { execFileSync } from "node:child_process";

const expected = {
  node: "24.18.0",
  npm: "11.16.0",
};

const actual = {
  node: process.versions.node,
  npm: readNpmVersion(),
};

const failures = Object.entries(expected)
  .filter(([tool, version]) => actual[tool] !== version)
  .map(([tool, version]) => `${tool} ${version} is required; found ${actual[tool]}`);

if (failures.length > 0) {
  console.error("frontend toolchain check failed:");
  for (const failure of failures) {
    console.error(`  - ${failure}`);
  }
  console.error("Install the pinned versions from .tool-versions, then run npm --prefix web ci.");
  process.exitCode = 1;
} else {
  console.log(`frontend toolchain: node ${actual.node}, npm ${actual.npm}`);
}

function readNpmVersion() {
  const userAgentVersion = process.env.npm_config_user_agent?.match(/\bnpm\/([^\s]+)/)?.[1];
  if (userAgentVersion) {
    return userAgentVersion;
  }

  const command = process.platform === "win32" ? "npm.cmd" : "npm";
  try {
    return execFileSync(command, ["--version"], { encoding: "utf8" }).trim();
  } catch {
    return "unavailable";
  }
}
