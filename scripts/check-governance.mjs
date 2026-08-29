import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const rootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const requiredFiles = [
  "LICENSE",
  "NOTICE",
  "DCO",
  "CONTRIBUTING.md",
  "CODE_OF_CONDUCT.md",
  "SECURITY.md",
  "SUPPORT.md",
  "ROADMAP.md",
  "docs/adr/README.md",
  "docs/architecture/README.md",
  "docs/development/AUTOMATION-DEPENDENCIES.md",
  "docs/development/DEPENDENCIES.md",
  ".github/workflows/dco.yml",
];

const requiredStatements = new Map([
  ["LICENSE", ["Apache License", "Version 2.0", "END OF TERMS AND CONDITIONS"]],
  ["NOTICE", ["Copyright", "does not grant permission to use the trade names"]],
  ["DCO", ["Developer's Certificate of Origin 1.1", "Signed-off-by:"]],
  [
    "CONTRIBUTING.md",
    ["Developer Certificate of Origin", "git commit --signoff", "No CLA"],
  ],
  [
    "SECURITY.md",
    [
      "privately",
      "codexfolio-support@venkatasudha.com",
      "Do not open a public issue",
      "credentials",
    ],
  ],
  ["SUPPORT.md", ["not affiliated with or endorsed by OpenAI", "Security"]],
  ["ROADMAP.md", ["MVP Roadmap", "GitHub issue #9"]],
  ["docs/adr/README.md", ["Superseding a decision", "user-visible guarantee"]],
  [
    "docs/development/AUTOMATION-DEPENDENCIES.md",
    [
      "actions/checkout",
      "Reviewed revision",
      "License",
      "Maintenance",
      "Exit strategy",
    ],
  ],
  [
    "docs/development/DEPENDENCIES.md",
    ["production need", "license", "maintenance", "lockfile"],
  ],
  [".github/workflows/dco.yml", ["pull_request", "check-dco.mjs"]],
]);

try {
  const failures = [];
  for (const file of requiredFiles) {
    if (!existsSync(join(rootDirectory, file))) {
      failures.push(`${file}: required governance file is missing`);
    }
  }

  for (const [file, statements] of requiredStatements) {
    const path = join(rootDirectory, file);
    if (!existsSync(path)) continue;
    const content = readFileSync(path, "utf8");
    for (const statement of statements) {
      if (
        !content.toLocaleLowerCase().includes(statement.toLocaleLowerCase())
      ) {
        failures.push(
          `${file}: required policy statement is missing: ${JSON.stringify(statement)}`,
        );
      }
    }
  }

  for (const markdownFile of findMarkdownFiles(rootDirectory)) {
    failures.push(...brokenLocalLinks(markdownFile));
  }

  if (failures.length > 0) {
    throw new Error(failures.join("\n"));
  }

  console.log(
    `governance check passed: ${requiredFiles.length} required files and local Markdown links are valid`,
  );
} catch (error) {
  console.error(
    `governance check failed:\n${error instanceof Error ? error.message : String(error)}`,
  );
  process.exitCode = 1;
}

function findMarkdownFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    if (
      entry.isDirectory() &&
      [".git", "build", "dist", "node_modules"].includes(entry.name)
    ) {
      return [];
    }
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return findMarkdownFiles(path);
    return extname(entry.name) === ".md" ? [path] : [];
  });
}

function brokenLocalLinks(markdownFile) {
  const content = readFileSync(markdownFile, "utf8");
  const failures = [];
  const links = content.matchAll(/(?<!!)\[[^\]]*\]\(([^)]+)\)/g);

  for (const match of links) {
    const rawTarget = match[1].trim().replace(/^<|>$/g, "");
    if (
      rawTarget === "" ||
      rawTarget.startsWith("#") ||
      /^[a-z][a-z\d+.-]*:/i.test(rawTarget) ||
      rawTarget.startsWith("//")
    ) {
      continue;
    }

    const pathTarget = decodeURIComponent(rawTarget.split("#", 1)[0]);
    const resolvedTarget = resolve(dirname(markdownFile), pathTarget);
    if (
      !resolvedTarget.startsWith(`${rootDirectory}${sep}`) &&
      resolvedTarget !== rootDirectory
    ) {
      failures.push(
        `${relative(rootDirectory, markdownFile)}: link escapes the repository: ${rawTarget}`,
      );
      continue;
    }
    if (!existsSync(resolvedTarget)) {
      failures.push(
        `${relative(rootDirectory, markdownFile)}: broken local link: ${rawTarget}`,
      );
      continue;
    }
    if (rawTarget.endsWith("/") && !statSync(resolvedTarget).isDirectory()) {
      failures.push(
        `${relative(rootDirectory, markdownFile)}: expected directory link: ${rawTarget}`,
      );
    }
  }
  return failures;
}
