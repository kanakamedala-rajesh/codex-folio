import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

export const GENERATOR_VERSION = "1.0.0";

const defaultRootDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const productVersionPath = "internal/buildinfo/version.txt";
const contractPath = "api/openapi.json";
const goOutputPath = "internal/httpapi/openapi.gen.go";
const typescriptOutputPath = "web/src/generated/openapi.ts";
const expectedTitle = "CodexFolio Local API";
const expectedOpenAPIVersion = "3.1.0";

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const options = readOptions(process.argv.slice(2));
    if (options.check) {
      checkOpenAPI(options.rootDirectory);
      console.log("OpenAPI artifacts are up to date");
    } else {
      writeArtifacts(generateOpenAPI(options.rootDirectory), options.rootDirectory);
      console.log(`generated OpenAPI artifacts with generator ${GENERATOR_VERSION}`);
    }
  } catch (error) {
    console.error(
      `OpenAPI generation failed: ${error instanceof Error ? error.message : String(error)}`,
    );
    process.exitCode = 1;
  }
}

export function generateOpenAPI(rootDirectory = defaultRootDirectory) {
  const productVersion = readProductVersion(rootDirectory);
  const contract = readContract(rootDirectory);
  const contractShape = validateContract(contract, productVersion);

  const sourceHash = createHash("sha256")
    .update(canonicalJSON(contract))
    .digest("hex");

  const artifacts = new Map([
    [
      goOutputPath,
      renderGo(
        productVersion,
        sourceHash,
        contractShape,
      ),
    ],
    [
      typescriptOutputPath,
      renderTypeScript(productVersion, sourceHash, contractShape),
    ],
  ]);

  return { artifacts, sourceHash };
}

export function checkOpenAPI(rootDirectory = defaultRootDirectory) {
  const result = generateOpenAPI(rootDirectory);
  checkArtifacts(result, rootDirectory);
  return result;
}

function readOptions(args) {
  let check = false;
  let rootDirectory = defaultRootDirectory;

  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--check") {
      if (check) {
        throw new Error("--check may only be provided once");
      }
      check = true;
      continue;
    }
    if (argument === "--root") {
      const root = args[index + 1]?.trim();
      if (!root) {
        throw new Error("--root requires a directory");
      }
      rootDirectory = resolve(root);
      index += 1;
      continue;
    }
    throw new Error(`unexpected argument ${JSON.stringify(argument)}`);
  }

  return { check, rootDirectory };
}

function readContract(rootDirectory) {
  const path = join(rootDirectory, contractPath);
  let source;
  try {
    source = readFileSync(path, "utf8");
  } catch (error) {
    throw new Error(`could not read ${contractPath}: ${error.message}`);
  }

  try {
    return JSON.parse(source);
  } catch (error) {
    throw new Error(`${contractPath} is not valid JSON: ${error.message}`);
  }
}

function readProductVersion(rootDirectory) {
  const path = join(rootDirectory, productVersionPath);
  let version;
  try {
    version = readFileSync(path, "utf8").trim();
  } catch (error) {
    throw new Error(`could not read ${productVersionPath}: ${error.message}`);
  }
  assertSemver(version, `canonical product version in ${productVersionPath}`);
  return version;
}

function validateContract(contract, productVersion) {
  assertObject(contract, "the OpenAPI document");
  assertEqual(contract.openapi, expectedOpenAPIVersion, "openapi version");
  assertObject(contract.info, "info");
  assertEqual(contract.info.title, expectedTitle, "info.title");
  assertEqual(contract.info.version, productVersion, "info.version (canonical product version)");
  const apiVersion = contract["x-codex-folio-api-version"];
  if (typeof apiVersion !== "string" || !/^v\d+$/.test(apiVersion)) {
    throw new Error("API version must use the v<major> format");
  }

  assertObject(contract.paths, "paths");
  assertExactKeyCount(contract.paths, 1, "paths");
  const [metadataPath, pathItem] = Object.entries(contract.paths)[0];
  assertEqual(metadataPath, `/api/${apiVersion}/meta`, "metadata path");
  assertObject(pathItem, `path ${metadataPath}`);
  assertExactKeys(pathItem, ["get"], `path ${metadataPath}`);
  assertObject(pathItem.get, `GET ${metadataPath}`);
  const operationId = pathItem.get.operationId;
  assertIdentifier(operationId, "operationId");

  assertObject(pathItem.get.responses, "metadata responses");
  assertExactKeys(pathItem.get.responses, ["200"], "metadata responses");
  const response = pathItem.get.responses["200"];
  assertObject(response, "metadata 200 response");
  assertObject(response.content, "metadata response content");
  assertExactKeys(response.content, ["application/json"], "metadata response content");
  const mediaType = response.content["application/json"];
  assertObject(mediaType, "metadata JSON response");
  assertObject(mediaType.schema, "metadata response schema");
  const responseReference = mediaType.schema.$ref;
  if (typeof responseReference !== "string" || !responseReference.startsWith("#/$defs/")) {
    throw new Error("metadata response reference must point to a local $defs schema");
  }
  const schemaName = responseReference.slice("#/$defs/".length);
  assertIdentifier(schemaName, "metadata schema name");

  assertObject(contract.$defs, "$defs");
  assertExactKeys(contract.$defs, [schemaName], "$defs");
  const metadataSchema = contract.$defs[schemaName];
  assertObject(metadataSchema, `${schemaName} schema`);
  assertEqual(metadataSchema.type, "object", `${schemaName} type`);
  assertEqual(metadataSchema.additionalProperties, false, `${schemaName} additionalProperties`);
  assertObject(metadataSchema.properties, `${schemaName} properties`);
  assertArrayEqual(
    metadataSchema.required,
    Object.keys(metadataSchema.properties),
    `${schemaName} required properties`,
  );
  const metadataFields = metadataSchema.required.map((name) => {
    if (!/^[a-z][a-z0-9_]*$/.test(name)) {
      throw new Error(`${schemaName} property ${JSON.stringify(name)} is not a snake_case field`);
    }
    return { name, schema: metadataSchema.properties[name] };
  });
  for (const { schema } of metadataFields) {
    assertEqual(schema.type, "string", `${schemaName} property type`);
  }

  return {
    apiVersion,
    metadataFields,
    metadataPath,
    operationId,
    responseReference,
    schemaName,
  };
}

function checkArtifacts({ artifacts }, rootDirectory) {
  const drifted = [];
  for (const [relativePath, expected] of artifacts) {
    const path = join(rootDirectory, relativePath);
    let actual;
    try {
      actual = readFileSync(path, "utf8");
    } catch {
      drifted.push(`${relativePath} is missing`);
      continue;
    }
    if (actual !== expected) {
      drifted.push(`${relativePath} differs from generated output`);
    }
  }
  if (drifted.length > 0) {
    throw new Error(`generated OpenAPI artifact drift detected:\n- ${drifted.join("\n- ")}`);
  }
}

function writeArtifacts({ artifacts }, rootDirectory = defaultRootDirectory) {
  for (const [relativePath, content] of artifacts) {
    const path = join(rootDirectory, relativePath);
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, content);
  }
}

function renderGo(productVersion, sourceHash, contractShape) {
  const {
    apiVersion,
    metadataFields,
    metadataPath,
    operationId,
    responseReference,
    schemaName,
  } = contractShape;
  const clientMethod = goIdentifier(operationId);
  const responseType = goIdentifier(schemaName);
  const fieldLines = metadataFields
    .map(({ name, schema }) => `\t${goIdentifier(name)} ${goType(schema)} \`json:"${name}"\``)
    .join("\n");

  const source = `// Code generated by codex-folio OpenAPI generator ${GENERATOR_VERSION}; DO NOT EDIT.
// Contract source: ${contractPath}
// Response schema: ${responseReference}
package httpapi

import (
\t"context"
\t"encoding/json"
\t"fmt"
\t"net/http"
\t"strings"
)

const (
\tAPIVersion           = "${apiVersion}"
\tContractVersion      = "${productVersion}"
\tContractSourceSHA256 = "${sourceHash}"
\tMetadataPath         = "${metadataPath}"
)

type ${responseType} struct {
${fieldLines}
}

type HTTPDoer interface {
\tDo(*http.Request) (*http.Response, error)
}

type Client struct {
\tbaseURL    string
\thttpClient HTTPDoer
}

func NewClient(baseURL string, httpClient HTTPDoer) *Client {
\treturn &Client{
\t\tbaseURL:    strings.TrimRight(baseURL, "/"),
\t\thttpClient: httpClient,
\t}
}

func (client *Client) ${clientMethod}(ctx context.Context) (${responseType}, *http.Response, error) {
\tvar result ${responseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+MetadataPath, nil)
\tif err != nil {
\t\treturn result, nil, err
\t}
\trequest.Header.Set("Accept", "application/json")

\thttpClient := client.httpClient
\tif httpClient == nil {
\t\thttpClient = http.DefaultClient
\t}
\tresponse, err := httpClient.Do(request)
\tif err != nil {
\t\treturn result, nil, err
\t}
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf(
\t\t\t"GET %s returned HTTP %d",
\t\t\tMetadataPath,
\t\t\tresponse.StatusCode,
\t\t)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil {
\t\treturn result, response, err
\t}
\treturn result, response, nil
}
`;

  return formatGo(source);
}

function renderTypeScript(productVersion, sourceHash, contractShape) {
  const {
    apiVersion,
    metadataFields,
    metadataPath,
    operationId,
    schemaName,
  } = contractShape;
  const responseType = schemaName;
  const fieldLines = metadataFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");

  return `// Code generated by codex-folio OpenAPI generator ${GENERATOR_VERSION}; DO NOT EDIT.
// Contract source: ${contractPath}
export const API_VERSION = "${apiVersion}" as const;
export const CONTRACT_VERSION = "${productVersion}" as const;
export const CONTRACT_SOURCE_SHA256 =
  "${sourceHash}" as const;

export interface ${responseType} {
${fieldLines}
}

export interface ApiPaths {
  "${metadataPath}": {
    get: {
      operationId: "${operationId}";
      responses: {
        200: {
          content: {
            "application/json": ${responseType};
          };
        };
      };
    };
  };
}

export interface CodexFolioApiClient {
  ${operationId}(init?: RequestInit): Promise<${responseType}>;
}

export function createCodexFolioApiClient(
  baseUrl = "",
  fetcher: typeof fetch = fetch,
): CodexFolioApiClient {
  return {
    async ${operationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${metadataPath}", {
        ...init,
        headers,
        method: "GET",
      });
      if (!response.ok) {
        throw new Error("GET ${metadataPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${responseType};
    },
  };
}
`;
}

function formatGo(source) {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), "codex-folio-openapi-gofmt-"));
  const temporaryPath = join(temporaryDirectory, "openapi.go");
  try {
    writeFileSync(temporaryPath, source);
    const result = spawnSync("gofmt", [temporaryPath], {
      encoding: "utf8",
    });
    if (result.status !== 0) {
      throw new Error(
        result.error?.message || `gofmt failed: ${result.stderr.trim()}`,
      );
    }
    return result.stdout;
  } finally {
    rmSync(temporaryDirectory, { force: true, recursive: true });
  }
}

function canonicalJSON(value) {
  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJSON(item)).join(",")}]`;
  }
  if (value !== null && typeof value === "object") {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`)
      .join(",")}}`;
  }
  return JSON.stringify(value);
}

function assertObject(value, name) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${name} must be an object`);
  }
}

function assertEqual(actual, expected, name) {
  if (actual !== expected) {
    throw new Error(`${name} must be ${JSON.stringify(expected)}`);
  }
}

function assertSemver(value, name) {
  const semanticVersion =
    /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
  if (typeof value !== "string" || !semanticVersion.test(value)) {
    throw new Error(`${name} must be a semantic version`);
  }
}

function assertExactKeys(value, expected, name) {
  assertObject(value, name);
  const actual = Object.keys(value).sort();
  const sortedExpected = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(sortedExpected)) {
    throw new Error(`${name} must contain exactly ${expected.join(", ")}`);
  }
}

function assertExactKeyCount(value, expected, name) {
  assertObject(value, name);
  if (Object.keys(value).length !== expected) {
    throw new Error(`${name} must contain exactly ${expected} key${expected === 1 ? "" : "s"}`);
  }
}

function assertIdentifier(value, name) {
  if (typeof value !== "string" || !/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(value)) {
    throw new Error(`${name} must be a valid identifier`);
  }
}

function assertArrayEqual(actual, expected, name) {
  if (!Array.isArray(actual) || JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`${name} must contain ${expected.join(", ")}`);
  }
}

function goIdentifier(name) {
  const identifier = name
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join("");
  return identifier.startsWith("Api") ? `API${identifier.slice(3)}` : identifier;
}

function goType(schema) {
  if (schema.type === "string") {
    return "string";
  }
  throw new Error(`unsupported Go schema type ${JSON.stringify(schema.type)}`);
}

function typescriptType(schema) {
  if (schema.type === "string") {
    return "string";
  }
  throw new Error(`unsupported TypeScript schema type ${JSON.stringify(schema.type)}`);
}
