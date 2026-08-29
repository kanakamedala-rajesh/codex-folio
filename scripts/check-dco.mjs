import { spawnSync } from "node:child_process";

try {
  const range = readRange(process.argv.slice(2));
  const commits = readCommits(range);
  const failures = commits.filter((commit) => !hasMatchingSignOff(commit));

  if (failures.length > 0) {
    for (const commit of failures) {
      console.error(
        `${commit.hash.slice(0, 12)} is missing a matching Signed-off-by trailer for ${commit.authorName} <${commit.authorEmail}>`,
      );
    }
    console.error(
      "DCO check failed; see CONTRIBUTING.md for correction instructions",
    );
    process.exitCode = 1;
  } else {
    console.log(
      `DCO check passed: ${commits.length} commit${commits.length === 1 ? "" : "s"}`,
    );
  }
} catch (error) {
  console.error(
    `DCO check failed: ${error instanceof Error ? error.message : String(error)}`,
  );
  process.exitCode = 1;
}

function readRange(args) {
  if (args.length === 2 && args[0] === "--range" && args[1].trim() !== "") {
    return args[1];
  }
  if (args.length === 0 && process.env.DCO_RANGE?.trim()) {
    return process.env.DCO_RANGE.trim();
  }
  throw new Error("provide a commit range with --range <range> or DCO_RANGE");
}

function readCommits(range) {
  const output = captureGit([
    "log",
    "--format=%H%x00%an%x00%ae%x00%B%x00%x1e",
    range,
  ]);

  return output
    .split("\x1e")
    .map((record) => record.trim())
    .filter(Boolean)
    .map((record) => {
      const [hash, authorName, authorEmail, ...bodyParts] = record.split("\0");
      return { hash, authorName, authorEmail, body: bodyParts.join("\0") };
    });
}

function hasMatchingSignOff({ authorName, authorEmail, body }) {
  const expected = `${authorName} <${authorEmail}>`.toLocaleLowerCase();
  const trailers = captureGit(["interpret-trailers", "--parse"], body);

  return trailers
    .split(/\r?\n/)
    .filter((line) => /^signed-off-by:/i.test(line))
    .some(
      (line) =>
        line
          .slice(line.indexOf(":") + 1)
          .trim()
          .toLocaleLowerCase() === expected,
    );
}

function captureGit(args, input) {
  const result = spawnSync("git", args, { encoding: "utf8", input });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(
      result.stderr.trim() ||
        `git ${args[0]} exited with status ${result.status}`,
    );
  }
  return result.stdout;
}
