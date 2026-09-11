import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, extname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const expectedVersion = readFileSync(
  resolve(scriptDirectory, "../../internal/buildinfo/version.txt"),
  "utf8",
).trim();

export function assertBuild(distDirectory) {
  const indexPath = join(distDirectory, "index.html");
  assert.ok(existsSync(indexPath), `frontend build is missing ${indexPath}`);

  const index = readFileSync(indexPath, "utf8");
  assert.match(index, /<div id="root"><\/div>/, "build is missing the application root");
  assert.doesNotMatch(index, /https?:\/\//i, "index.html must not use hosted resources");

  const references = [...index.matchAll(/(?:src|href)="([^"]+)"/g)].map((match) => match[1]);
  for (const reference of references) {
    if (reference.startsWith("#") || reference.startsWith("data:")) {
      continue;
    }
    assert.doesNotMatch(
      reference,
      /^(?:https?:)?\/\//i,
      `asset reference is not local: ${reference}`,
    );
    const assetPath = resolve(distDirectory, reference.replace(/^(?:\.\/|\/assets\/)/, ""));
    assert.ok(existsSync(assetPath), `index.html references missing asset ${reference}`);
  }

  const textFiles = collectFiles(distDirectory).filter((filePath) =>
    [".css", ".html", ".js"].includes(extname(filePath)),
  );
  assert.ok(
    textFiles.some((filePath) => extname(filePath) === ".js"),
    "build is missing JavaScript",
  );
  assert.ok(
    textFiles.some((filePath) => extname(filePath) === ".css"),
    "build is missing CSS",
  );

  const text = textFiles.map((filePath) => readFileSync(filePath, "utf8")).join("\n");
  assert.match(text, /VenkataSudha CodexFolio/, "built app is missing the product name");
  assert.match(
    text,
    new RegExp(escapeRegExp(expectedVersion)),
    "built app is missing its product version",
  );
  assert.match(text, /Current capacity/, "built app is missing its Overview surface");
  assert.doesNotMatch(
    text,
    /(?:import|fetch)\s*\(\s*["'](?:https?:)?\/\//i,
    "built JavaScript must not load external modules or data",
  );
  assert.doesNotMatch(
    text,
    /fonts\.googleapis\.com|@import\s+url\(/i,
    "built assets must not use hosted fonts",
  );
}

function collectFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? collectFiles(path) : [path];
  });
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const distDirectory = resolve(
    process.argv[2] ?? resolve(scriptDirectory, "../../internal/httpapi/assets"),
  );
  try {
    assertBuild(distDirectory);
    console.log(`frontend smoke: ${relative(process.cwd(), distDirectory)} is self-contained`);
  } catch (error) {
    console.error(`frontend smoke failed: ${error.message}`);
    process.exitCode = 1;
  }
}
