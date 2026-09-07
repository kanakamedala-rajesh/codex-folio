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

  const activityPath = `/api/${apiVersion}/activity`;
  const analyticsPath = `/api/${apiVersion}/analytics`;
  const bootstrapPath = `/api/${apiVersion}/bootstrap`;
  const metadataPath = `/api/${apiVersion}/meta`;
  const projectsPath = `/api/${apiVersion}/projects`;
  const selectionPath = `/api/${apiVersion}/selection`;
  const usageLatestPath = `/api/${apiVersion}/usage/latest`;
  const usageRefreshPath = `/api/${apiVersion}/usage/refresh`;
  assertObject(contract.paths, "paths");
  assertExactKeys(contract.paths, [activityPath, analyticsPath, bootstrapPath, metadataPath, projectsPath, selectionPath, usageLatestPath, usageRefreshPath], "paths");

  const bootstrapPathItem = contract.paths[bootstrapPath];
  assertObject(bootstrapPathItem, `path ${bootstrapPath}`);
  assertExactKeys(bootstrapPathItem, ["post"], `path ${bootstrapPath}`);
  const bootstrapOperation = bootstrapPathItem.post;
  assertObject(bootstrapOperation, `POST ${bootstrapPath}`);
  assertExactKeys(
    bootstrapOperation,
    ["operationId", "requestBody", "responses"],
    `POST ${bootstrapPath}`,
  );
  const bootstrapOperationId = bootstrapOperation.operationId;
  assertIdentifier(bootstrapOperationId, "bootstrap operationId");
  const bootstrapRequestReference = requestReference(
    bootstrapOperation.requestBody,
    `POST ${bootstrapPath} request body`,
  );
  const bootstrapResponseReference = responseReference(
    bootstrapOperation,
    `POST ${bootstrapPath}`,
  );

  const metadataPathItem = contract.paths[metadataPath];
  assertObject(metadataPathItem, `path ${metadataPath}`);
  assertExactKeys(metadataPathItem, ["get"], `path ${metadataPath}`);
  const metadataOperation = metadataPathItem.get;
  assertObject(metadataOperation, `GET ${metadataPath}`);
  assertExactKeys(metadataOperation, ["operationId", "responses"], `GET ${metadataPath}`);
  const metadataOperationId = metadataOperation.operationId;
  assertIdentifier(metadataOperationId, "metadata operationId");
  const metadataResponseReference = responseReference(
    metadataOperation,
    `GET ${metadataPath}`,
  );

  const activityOperation = contract.paths[activityPath]?.get;
  assertObject(activityOperation, `GET ${activityPath}`);
  assertExactKeys(activityOperation, ["operationId", "parameters", "responses"], `GET ${activityPath}`);
  assertIdentifier(activityOperation.operationId, "activity operationId");
  if (!Array.isArray(activityOperation.parameters) || activityOperation.parameters.length !== 2) {
    throw new Error(`GET ${activityPath} must declare profile and project query parameters`);
  }
  const activityResponseReference = responseReference(activityOperation, `GET ${activityPath}`);

  const analyticsOperation = contract.paths[analyticsPath]?.get;
  assertObject(analyticsOperation, `GET ${analyticsPath}`);
  assertExactKeys(analyticsOperation, ["operationId", "parameters", "responses"], `GET ${analyticsPath}`);
  assertIdentifier(analyticsOperation.operationId, "analytics operationId");
  if (!Array.isArray(analyticsOperation.parameters) || analyticsOperation.parameters.length !== 1 || analyticsOperation.parameters[0]?.name !== "scope") {
    throw new Error(`GET ${analyticsPath} must declare the optional scope query parameter`);
  }
  const analyticsResponseReference = responseReference(analyticsOperation, `GET ${analyticsPath}`, ["200", "default"]);

  const selectionPathItem = contract.paths[selectionPath];
  assertObject(selectionPathItem, `path ${selectionPath}`);
  assertExactKeys(selectionPathItem, ["get", "put"], `path ${selectionPath}`);
  const getSelectionOperation = selectionPathItem.get;
  const setSelectionOperation = selectionPathItem.put;
  assertExactKeys(getSelectionOperation, ["operationId", "responses"], `GET ${selectionPath}`);
  assertExactKeys(setSelectionOperation, ["operationId", "requestBody", "responses"], `PUT ${selectionPath}`);
  assertIdentifier(getSelectionOperation.operationId, "get selection operationId");
  assertIdentifier(setSelectionOperation.operationId, "set selection operationId");
  const selectionRequestReference = requestReference(setSelectionOperation.requestBody, `PUT ${selectionPath} request body`);
  const selectionResponseReference = responseReference(getSelectionOperation, `GET ${selectionPath}`);
  assertEqual(responseReference(setSelectionOperation, `PUT ${selectionPath}`), selectionResponseReference, "selection response reference");

  const projectsOperation = contract.paths[projectsPath]?.get;
  assertObject(projectsOperation, `GET ${projectsPath}`);
  assertExactKeys(projectsOperation, ["operationId", "responses"], `GET ${projectsPath}`);
  assertIdentifier(projectsOperation.operationId, "projects operationId");
  const projectsResponseReference = responseReference(projectsOperation, `GET ${projectsPath}`);

  const usageOperation = contract.paths[usageRefreshPath]?.post;
  assertObject(usageOperation, `POST ${usageRefreshPath}`);
  assertExactKeys(usageOperation, ["operationId", "requestBody", "responses"], `POST ${usageRefreshPath}`);
  assertIdentifier(usageOperation.operationId, "usage refresh operationId");
  const usageRequestReference = requestReference(usageOperation.requestBody, `POST ${usageRefreshPath} request body`);
  const usageResponseReference = responseReference(usageOperation, `POST ${usageRefreshPath}`, ["200", "default"]);
  const usageErrorResponseReference = errorResponseReference(usageOperation, `POST ${usageRefreshPath}`);
  const usageLatestOperation = contract.paths[usageLatestPath]?.get;
  assertObject(usageLatestOperation, `GET ${usageLatestPath}`);
  assertExactKeys(usageLatestOperation, ["operationId", "parameters", "responses"], `GET ${usageLatestPath}`);
  assertIdentifier(usageLatestOperation.operationId, "latest usage operationId");
  if (!Array.isArray(usageLatestOperation.parameters) || usageLatestOperation.parameters.length !== 1 || usageLatestOperation.parameters[0]?.name !== "alias") {
    throw new Error(`GET ${usageLatestPath} must declare the alias query parameter`);
  }
  assertEqual(responseReference(usageLatestOperation, `GET ${usageLatestPath}`, ["200", "default"]), usageResponseReference, "latest usage response reference");
  assertEqual(errorResponseReference(usageLatestOperation, `GET ${usageLatestPath}`), usageErrorResponseReference, "latest usage error response reference");
  assertEqual(errorResponseReference(analyticsOperation, `GET ${analyticsPath}`), usageErrorResponseReference, "analytics error response reference");

  const schemaNames = [
    schemaNameFromReference(bootstrapRequestReference, "bootstrap request"),
    schemaNameFromReference(bootstrapResponseReference, "bootstrap response"),
    schemaNameFromReference(metadataResponseReference, "metadata response"),
    schemaNameFromReference(selectionRequestReference, "selection request"),
    schemaNameFromReference(selectionResponseReference, "selection response"),
    schemaNameFromReference(projectsResponseReference, "projects response"),
    schemaNameFromReference(usageRequestReference, "usage request"),
    schemaNameFromReference(usageResponseReference, "usage response"),
    schemaNameFromReference(usageErrorResponseReference, "usage error response"),
    schemaNameFromReference(activityResponseReference, "activity response"),
    "UsageObservation",
    "UsageMetricAvailability",
    "ProjectIdentity",
    "ActivityRecord",
    schemaNameFromReference(analyticsResponseReference, "analytics response"),
    "UsageAggregate",
    "UsageMetricAmbiguity",
  ];
  assertObject(contract.$defs, "$defs");
  assertExactKeys(contract.$defs, schemaNames, "$defs");
  const bootstrapRequestFields = schemaFields(
    contract.$defs[schemaNames[0]],
    schemaNames[0],
  );
  const bootstrapResponseFields = schemaFields(
    contract.$defs[schemaNames[1]],
    schemaNames[1],
  );
  const metadataFields = schemaFields(contract.$defs[schemaNames[2]], schemaNames[2]);
  const selectionRequestFields = schemaFields(contract.$defs[schemaNames[3]], schemaNames[3]);
  const selectionResponseFields = schemaFields(contract.$defs[schemaNames[4]], schemaNames[4]);
  const projectsResponseFields = schemaFields(contract.$defs[schemaNames[5]], schemaNames[5]);
  const usageRequestFields = schemaFields(contract.$defs[schemaNames[6]], schemaNames[6]);
  const usageResponseFields = schemaFields(contract.$defs[schemaNames[7]], schemaNames[7]);
  const usageErrorResponseFields = schemaFields(contract.$defs[schemaNames[8]], schemaNames[8]);
  const usageObservationFields = schemaFields(contract.$defs.UsageObservation, "UsageObservation");
  const usageAvailabilityFields = schemaFields(contract.$defs.UsageMetricAvailability, "UsageMetricAvailability");
  const projectIdentityFields = schemaFields(contract.$defs.ProjectIdentity, "ProjectIdentity");
  const activityResponseFields = schemaFields(contract.$defs[schemaNames[9]], schemaNames[9]);
  const activityRecordFields = schemaFields(contract.$defs.ActivityRecord, "ActivityRecord");
  const analyticsResponseFields = schemaFields(contract.$defs[schemaNames[14]], schemaNames[14]);
  const usageAggregateFields = schemaFields(contract.$defs.UsageAggregate, "UsageAggregate");
  const usageMetricAmbiguityFields = schemaFields(contract.$defs.UsageMetricAmbiguity, "UsageMetricAmbiguity");

  return {
    apiVersion,
    activityOperationId: activityOperation.operationId,
    activityPath,
    activityRecordFields,
    activityRecordType: "ActivityRecord",
    activityResponseFields,
    activityResponseType: schemaNames[9],
    analyticsOperationId: analyticsOperation.operationId,
    analyticsPath,
    analyticsResponseFields,
    analyticsResponseType: schemaNames[14],
    bootstrapPath,
    bootstrapOperationId,
    bootstrapRequestFields,
    bootstrapRequestType: schemaNames[0],
    bootstrapResponseFields,
    bootstrapResponseType: schemaNames[1],
    metadataFields,
    metadataPath,
    metadataOperationId,
    metadataResponseType: schemaNames[2],
    projectIdentityFields,
    projectIdentityType: "ProjectIdentity",
    projectsOperationId: projectsOperation.operationId,
    projectsPath,
    projectsResponseFields,
    projectsResponseType: schemaNames[5],
    selectionGetOperationId: getSelectionOperation.operationId,
    selectionPath,
    selectionRequestFields,
    selectionRequestType: schemaNames[3],
    selectionResponseFields,
    selectionResponseType: schemaNames[4],
    selectionSetOperationId: setSelectionOperation.operationId,
    usageAvailabilityFields,
    usageAvailabilityType: "UsageMetricAvailability",
    usageAggregateFields,
    usageAggregateType: "UsageAggregate",
    usageMetricAmbiguityFields,
    usageMetricAmbiguityType: "UsageMetricAmbiguity",
    usageObservationFields,
    usageObservationType: "UsageObservation",
    usageLatestOperationId: usageLatestOperation.operationId,
    usageLatestPath,
    usageOperationId: usageOperation.operationId,
    usageErrorResponseFields,
    usageErrorResponseType: schemaNames[8],
    usageRefreshPath,
    usageRequestFields,
    usageRequestType: schemaNames[6],
    usageResponseFields,
    usageResponseType: schemaNames[7],
  };
}

function requestReference(requestBody, name) {
  assertObject(requestBody, name);
  assertExactKeys(requestBody, ["content", "required"], name);
  assertEqual(requestBody.required, true, `${name}.required`);
  assertObject(requestBody.content, `${name}.content`);
  assertExactKeys(requestBody.content, ["application/json"], `${name}.content`);
  const mediaType = requestBody.content["application/json"];
  assertObject(mediaType, `${name} JSON content`);
  assertObject(mediaType.schema, `${name} schema`);
  return mediaType.schema.$ref;
}

function responseReference(operation, name, expectedStatuses = ["200"]) {
  assertObject(operation.responses, `${name} responses`);
  assertExactKeys(operation.responses, expectedStatuses, `${name} responses`);
  const response = operation.responses["200"];
  assertObject(response, `${name} 200 response`);
  assertObject(response.content, `${name} response content`);
  assertExactKeys(response.content, ["application/json"], `${name} response content`);
  const mediaType = response.content["application/json"];
  assertObject(mediaType, `${name} JSON response`);
  assertObject(mediaType.schema, `${name} response schema`);
  return mediaType.schema.$ref;
}

function errorResponseReference(operation, name) {
  const response = operation.responses.default;
  assertObject(response, `${name} default response`);
  assertObject(response.content, `${name} default response content`);
  assertExactKeys(response.content, ["application/json"], `${name} default response content`);
  const mediaType = response.content["application/json"];
  assertObject(mediaType, `${name} default JSON response`);
  assertObject(mediaType.schema, `${name} default response schema`);
  return mediaType.schema.$ref;
}

function schemaNameFromReference(reference, name) {
  if (typeof reference !== "string" || !reference.startsWith("#/$defs/")) {
    throw new Error(`${name} reference must point to a local $defs schema`);
  }
  const schemaName = reference.slice("#/$defs/".length);
  assertIdentifier(schemaName, `${name} schema name`);
  return schemaName;
}

function schemaFields(schema, schemaName) {
  assertObject(schema, `${schemaName} schema`);
  assertEqual(schema.type, "object", `${schemaName} type`);
  assertEqual(schema.additionalProperties, false, `${schemaName} additionalProperties`);
  assertObject(schema.properties, `${schemaName} properties`);
  if (!Array.isArray(schema.required) || schema.required.some((name) => !Object.hasOwn(schema.properties, name))) {
    throw new Error(`${schemaName} required properties must name declared properties`);
  }
  const required = new Set(schema.required);
  const fields = Object.keys(schema.properties).map((name) => {
    if (!/^[a-z][a-z0-9_]*$/.test(name)) {
      throw new Error(`${schemaName} property ${JSON.stringify(name)} is not a snake_case field`);
    }
    return { name, required: required.has(name), schema: schema.properties[name] };
  });
  return fields;
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
    activityOperationId,
    activityPath,
    activityRecordFields,
    activityRecordType,
    activityResponseFields,
    activityResponseType,
    analyticsOperationId,
    analyticsPath,
    analyticsResponseFields,
    analyticsResponseType,
    bootstrapOperationId,
    bootstrapPath,
    bootstrapRequestFields,
    bootstrapRequestType,
    bootstrapResponseFields,
    bootstrapResponseType,
    metadataFields,
    metadataPath,
    metadataOperationId,
    metadataResponseType,
    projectIdentityFields,
    projectIdentityType,
    projectsOperationId,
    projectsPath,
    projectsResponseFields,
    projectsResponseType,
    selectionGetOperationId,
    selectionPath,
    selectionRequestFields,
    selectionRequestType,
    selectionResponseFields,
    selectionResponseType,
    selectionSetOperationId,
    usageAvailabilityFields,
    usageAvailabilityType,
    usageAggregateFields,
    usageAggregateType,
    usageMetricAmbiguityFields,
    usageMetricAmbiguityType,
    usageObservationFields,
    usageObservationType,
    usageLatestOperationId,
    usageLatestPath,
    usageOperationId,
    usageErrorResponseFields,
    usageErrorResponseType,
    usageRefreshPath,
    usageRequestFields,
    usageRequestType,
    usageResponseFields,
    usageResponseType,
  } = contractShape;
  const activityMethod = goIdentifier(activityOperationId);
  const analyticsMethod = goIdentifier(analyticsOperationId);
  const bootstrapMethod = goIdentifier(bootstrapOperationId);
  const metadataMethod = goIdentifier(metadataOperationId);
  const projectsMethod = goIdentifier(projectsOperationId);
  const selectionGetMethod = goIdentifier(selectionGetOperationId);
  const selectionSetMethod = goIdentifier(selectionSetOperationId);
  const usageLatestMethod = goIdentifier(usageLatestOperationId);
  const usageMethod = goIdentifier(usageOperationId);
  const types = [
    renderGoStruct(activityRecordType, activityRecordFields),
    renderGoStruct(activityResponseType, activityResponseFields),
    renderGoStruct(analyticsResponseType, analyticsResponseFields),
    renderGoStruct(bootstrapRequestType, bootstrapRequestFields),
    renderGoStruct(bootstrapResponseType, bootstrapResponseFields),
    renderGoStruct(metadataResponseType, metadataFields),
    renderGoStruct(projectIdentityType, projectIdentityFields),
    renderGoStruct(projectsResponseType, projectsResponseFields),
    renderGoStruct(selectionRequestType, selectionRequestFields),
    renderGoStruct(selectionResponseType, selectionResponseFields),
    renderGoStruct(usageRequestType, usageRequestFields),
    renderGoStruct(usageErrorResponseType, usageErrorResponseFields),
    renderGoStruct(usageObservationType, usageObservationFields),
    renderGoStruct(usageAvailabilityType, usageAvailabilityFields),
    renderGoStruct(usageAggregateType, usageAggregateFields),
    renderGoStruct(usageMetricAmbiguityType, usageMetricAmbiguityFields),
    renderGoStruct(usageResponseType, usageResponseFields),
  ].join("\n\n");

  const source = `// Code generated by codex-folio OpenAPI generator ${GENERATOR_VERSION}; DO NOT EDIT.
// Contract source: ${contractPath}
// Response schemas: ${activityResponseType}, ${bootstrapResponseType}, ${metadataResponseType}, ${projectsResponseType}, ${selectionResponseType}, ${usageResponseType}
package httpapi

import (
\t"bytes"
\t"context"
\t"encoding/json"
\t"fmt"
\t"net/http"
\t"net/url"
\t"strings"
)

const (
\tAPIVersion           = "${apiVersion}"
\tActivityPath         = "${activityPath}"
\tAnalyticsPath        = "${analyticsPath}"
\tContractVersion      = "${productVersion}"
\tContractSourceSHA256 = "${sourceHash}"
\tBootstrapPath        = "${bootstrapPath}"
\tMetadataPath         = "${metadataPath}"
\tProjectsPath         = "${projectsPath}"
\tSelectionPath        = "${selectionPath}"
\tUsageLatestPath      = "${usageLatestPath}"
\tUsageRefreshPath     = "${usageRefreshPath}"
)

${types}

func (response ${usageErrorResponseType}) Error() string { return response.Code }

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

func (client *Client) ${activityMethod}(ctx context.Context, profileAlias, projectID string) (${activityResponseType}, *http.Response, error) {
\tvar result ${activityResponseType}
\tquery := url.Values{}
\tif profileAlias != "" { query.Set("profile", profileAlias) }
\tif projectID != "" { query.Set("project", projectID) }
\tpath := ActivityPath
\tif encoded := query.Encode(); encoded != "" { path += "?" + encoded }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf("GET %s returned HTTP %d", ActivityPath, response.StatusCode)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}

func (client *Client) ${analyticsMethod}(ctx context.Context, scope string) (${analyticsResponseType}, *http.Response, error) {
\tvar result ${analyticsResponseType}
\tpath := AnalyticsPath
\tif scope != "" { path += "?" + url.Values{"scope": []string{scope}}.Encode() }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
\t\t\treturn result, response, fmt.Errorf("GET %s returned HTTP %d", AnalyticsPath, response.StatusCode)
\t\t}
\t\treturn result, response, failure
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}

func (client *Client) ${bootstrapMethod}(ctx context.Context, input ${bootstrapRequestType}) (${bootstrapResponseType}, *http.Response, error) {
\tvar result ${bootstrapResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil {
\t\treturn result, nil, err
\t}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+BootstrapPath, bytes.NewReader(body))
\tif err != nil {
\t\treturn result, nil, err
\t}
\trequest.Header.Set("Accept", "application/json")
\trequest.Header.Set("Content-Type", "application/json")

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
\t\t\t"POST %s returned HTTP %d",
\t\t\tBootstrapPath,
\t\t\tresponse.StatusCode,
\t\t)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil {
\t\treturn result, response, err
\t}
\treturn result, response, nil
}

func (client *Client) ${metadataMethod}(ctx context.Context) (${metadataResponseType}, *http.Response, error) {
\tvar result ${metadataResponseType}
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
\t\t\t)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil {
\t\treturn result, response, err
\t}
\treturn result, response, nil
}

func (client *Client) ${projectsMethod}(ctx context.Context) (${projectsResponseType}, *http.Response, error) {
\tvar result ${projectsResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+ProjectsPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf("GET %s returned HTTP %d", ProjectsPath, response.StatusCode)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}

func (client *Client) ${selectionGetMethod}(ctx context.Context) (${selectionResponseType}, *http.Response, error) {
\tvar result ${selectionResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+SelectionPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf("GET %s returned HTTP %d", SelectionPath, response.StatusCode)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}

func (client *Client) ${selectionSetMethod}(ctx context.Context, input ${selectionRequestType}) (${selectionResponseType}, *http.Response, error) {
\tvar result ${selectionResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPut, client.baseURL+SelectionPath, bytes.NewReader(body))
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\trequest.Header.Set("Content-Type", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf("PUT %s returned HTTP %d", SelectionPath, response.StatusCode)
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}

func (client *Client) ${usageMethod}(ctx context.Context, input ${usageRequestType}) (${usageResponseType}, *http.Response, error) {
\tvar result ${usageResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+UsageRefreshPath, bytes.NewReader(body))
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\trequest.Header.Set("Content-Type", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
\t\t\treturn result, response, fmt.Errorf("POST %s returned HTTP %d", UsageRefreshPath, response.StatusCode)
\t\t}
\t\treturn result, response, failure
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}
func (client *Client) ${usageLatestMethod}(ctx context.Context, alias string) (${usageResponseType}, *http.Response, error) {
\tvar result ${usageResponseType}
\tquery := url.Values{"alias": []string{alias}}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+UsageLatestPath+"?"+query.Encode(), nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
\t\t\treturn result, response, fmt.Errorf("GET %s returned HTTP %d", UsageLatestPath, response.StatusCode)
\t\t}
\t\treturn result, response, failure
\t}
\tif err := json.NewDecoder(response.Body).Decode(&result); err != nil { return result, response, err }
\treturn result, response, nil
}

`;

  return formatGo(source);
}

function renderGoStruct(schemaName, fields) {
  const fieldLines = fields
    .map(({ name, required, schema }) => `\t${goIdentifier(name)} ${required ? "" : "*"}${goType(schema)} \`json:"${name}${required ? "" : ",omitempty"}"\``)
    .join("\n");
  return `type ${goIdentifier(schemaName)} struct {
${fieldLines}
}`;
}

function renderTypeScript(productVersion, sourceHash, contractShape) {
  const {
    apiVersion,
    activityOperationId,
    activityPath,
    activityRecordFields,
    activityRecordType,
    activityResponseFields,
    activityResponseType,
    analyticsOperationId,
    analyticsPath,
    analyticsResponseFields,
    analyticsResponseType,
    bootstrapOperationId,
    bootstrapPath,
    bootstrapRequestFields,
    bootstrapRequestType,
    bootstrapResponseFields,
    bootstrapResponseType,
    metadataFields,
    metadataPath,
    metadataOperationId,
    metadataResponseType,
    projectIdentityFields,
    projectIdentityType,
    projectsOperationId,
    projectsPath,
    projectsResponseFields,
    projectsResponseType,
    selectionGetOperationId,
    selectionPath,
    selectionRequestFields,
    selectionRequestType,
    selectionResponseFields,
    selectionResponseType,
    selectionSetOperationId,
    usageAvailabilityFields,
    usageAvailabilityType,
    usageAggregateFields,
    usageAggregateType,
    usageMetricAmbiguityFields,
    usageMetricAmbiguityType,
    usageObservationFields,
    usageObservationType,
    usageLatestOperationId,
    usageLatestPath,
    usageOperationId,
    usageErrorResponseFields,
    usageErrorResponseType,
    usageRefreshPath,
    usageRequestFields,
    usageRequestType,
    usageResponseFields,
    usageResponseType,
  } = contractShape;
  const activityRecordLines = activityRecordFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const activityResponseLines = activityResponseFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const analyticsResponseLines = analyticsResponseFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const bootstrapRequestLines = bootstrapRequestFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const bootstrapResponseLines = bootstrapResponseFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const metadataLines = metadataFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const projectIdentityLines = projectIdentityFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const projectsResponseLines = projectsResponseFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const selectionRequestLines = selectionRequestFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const selectionResponseLines = selectionResponseFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const usageRequestLines = usageRequestFields.map(({ name, required, schema }) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n");
  const usageErrorResponseLines = usageErrorResponseFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const usageObservationLines = usageObservationFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const usageAvailabilityLines = usageAvailabilityFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const usageAggregateLines = usageAggregateFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const usageMetricAmbiguityLines = usageMetricAmbiguityFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const usageResponseLines = usageResponseFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");

  return `// Code generated by codex-folio OpenAPI generator ${GENERATOR_VERSION}; DO NOT EDIT.
// Contract source: ${contractPath}
export const API_VERSION = "${apiVersion}" as const;
export const CONTRACT_VERSION = "${productVersion}" as const;
export const CONTRACT_SOURCE_SHA256 =
  "${sourceHash}" as const;

export interface ${activityRecordType} {
${activityRecordLines}
}

export interface ${activityResponseType} {
${activityResponseLines}
}

export interface ${analyticsResponseType} {
${analyticsResponseLines}
}

export interface ${bootstrapRequestType} {
${bootstrapRequestLines}
}

export interface ${bootstrapResponseType} {
${bootstrapResponseLines}
}

export interface ${metadataResponseType} {
${metadataLines}
}

export interface ${projectIdentityType} {
${projectIdentityLines}
}

export interface ${projectsResponseType} {
${projectsResponseLines}
}

export interface ${selectionRequestType} {
${selectionRequestLines}
}

export interface ${selectionResponseType} {
${selectionResponseLines}
}

export interface ${usageRequestType} {
${usageRequestLines}
}

export interface ${usageErrorResponseType} {
${usageErrorResponseLines}
}

export class UsageRefreshError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, status: number, message: string) {
    super(message);
    this.code = code;
    this.name = "UsageRefreshError";
    this.status = status;
  }
}

export interface ${usageObservationType} {
${usageObservationLines}
}

export interface ${usageAvailabilityType} {
${usageAvailabilityLines}
}

export interface ${usageAggregateType} {
${usageAggregateLines}
}

export interface ${usageMetricAmbiguityType} {
${usageMetricAmbiguityLines}
}

export interface ${usageResponseType} {
${usageResponseLines}
}

export interface ApiPaths {
  "${analyticsPath}": {
    get: {
      operationId: "${analyticsOperationId}";
      responses: {
        200: { content: { "application/json": ${analyticsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${activityPath}": {
    get: {
      operationId: "${activityOperationId}";
      responses: { 200: { content: { "application/json": ${activityResponseType} } } };
    };
  };
  "${bootstrapPath}": {
    post: {
      operationId: "${bootstrapOperationId}";
      requestBody: ${bootstrapRequestType};
      responses: {
        200: {
          content: {
            "application/json": ${bootstrapResponseType};
          };
        };
      };
    };
  };
  "${metadataPath}": {
    get: {
      operationId: "${metadataOperationId}";
      responses: {
        200: {
          content: {
            "application/json": ${metadataResponseType};
          };
        };
      };
    };
  };
  "${selectionPath}": {
    get: {
      operationId: "${selectionGetOperationId}";
      responses: { 200: { content: { "application/json": ${selectionResponseType} } } };
    };
    put: {
      operationId: "${selectionSetOperationId}";
      requestBody: ${selectionRequestType};
      responses: { 200: { content: { "application/json": ${selectionResponseType} } } };
    };
  };
  "${projectsPath}": {
    get: {
      operationId: "${projectsOperationId}";
      responses: { 200: { content: { "application/json": ${projectsResponseType} } } };
    };
  };
  "${usageRefreshPath}": {
    post: {
      operationId: "${usageOperationId}";
      requestBody: ${usageRequestType};
      responses: {
        200: { content: { "application/json": ${usageResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${usageLatestPath}": {
    get: {
      operationId: "${usageLatestOperationId}";
      responses: {
        200: { content: { "application/json": ${usageResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
}

export interface CodexFolioApiClient {
  ${analyticsOperationId}(scope?: string, init?: RequestInit): Promise<${analyticsResponseType}>;
  ${activityOperationId}(
    profileAlias?: string,
    projectId?: string,
    init?: RequestInit,
  ): Promise<${activityResponseType}>;
  ${bootstrapOperationId}(request: ${bootstrapRequestType}, init?: RequestInit): Promise<${bootstrapResponseType}>;
  ${metadataOperationId}(init?: RequestInit): Promise<${metadataResponseType}>;
  ${projectsOperationId}(init?: RequestInit): Promise<${projectsResponseType}>;
  ${selectionGetOperationId}(init?: RequestInit): Promise<${selectionResponseType}>;
  ${selectionSetOperationId}(request: ${selectionRequestType}, init?: RequestInit): Promise<${selectionResponseType}>;
  ${usageLatestOperationId}(alias: string, init?: RequestInit): Promise<${usageResponseType}>;
  ${usageOperationId}(request: ${usageRequestType}, init?: RequestInit): Promise<${usageResponseType}>;
}

export function createCodexFolioApiClient(
  baseUrl = "",
  fetcher: typeof fetch = fetch,
): CodexFolioApiClient {
  return {
    async ${analyticsOperationId}(scope = "", init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const query = new URLSearchParams();
      if (scope) query.set("scope", scope);
      const suffix = query.size ? "?" + query.toString() : "";
      const response = await fetcher(baseUrl + "${analyticsPath}" + suffix, {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${analyticsResponseType};
    },
    async ${activityOperationId}(profileAlias = "", projectId = "", init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const query = new URLSearchParams();
      if (profileAlias) query.set("profile", profileAlias);
      if (projectId) query.set("project", projectId);
      const suffix = query.size ? "?" + query.toString() : "";
      const response = await fetcher(baseUrl + "${activityPath}" + suffix, {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        throw new Error("GET ${activityPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${activityResponseType};
    },
    async ${bootstrapOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${bootstrapPath}", {
        ...init,
        body: JSON.stringify(request),
        credentials: init.credentials ?? "include",
        headers,
        method: "POST",
      });
      if (!response.ok) {
        throw new Error("POST ${bootstrapPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${bootstrapResponseType};
    },
    async ${metadataOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${metadataPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        throw new Error("GET ${metadataPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${metadataResponseType};
    },
    async ${projectsOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${projectsPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        throw new Error("GET ${projectsPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${projectsResponseType};
    },
    async ${selectionGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${selectionPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        throw new Error("GET ${selectionPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${selectionResponseType};
    },
    async ${selectionSetOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${selectionPath}", {
        ...init,
        body: JSON.stringify(request),
        credentials: init.credentials ?? "include",
        headers,
        method: "PUT",
      });
      if (!response.ok) {
        throw new Error("PUT ${selectionPath} failed with HTTP " + response.status);
      }
      return (await response.json()) as ${selectionResponseType};
    },
    async ${usageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${usageRefreshPath}", {
        ...init,
        body: JSON.stringify(request),
        credentials: init.credentials ?? "include",
        headers,
        method: "POST",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${usageResponseType};
    },
    async ${usageLatestOperationId}(alias, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const query = new URLSearchParams({ alias });
      const response = await fetcher(baseUrl + "${usageLatestPath}?" + query.toString(), {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${usageResponseType};
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
  if (identifier.startsWith("Csrf")) {
    return `CSRF${identifier.slice("Csrf".length)}`;
  }
  return identifier.startsWith("Api") ? `API${identifier.slice(3)}` : identifier;
}

function goType(schema) {
  if (typeof schema.$ref === "string") {
    return goIdentifier(schemaNameFromReference(schema.$ref, "field"));
  }
  if (schema.type === "string") {
    return "string";
  }
  if (schema.type === "number") {
    return "float64";
  }
  if (schema.type === "integer") {
    return "int64";
  }
  if (schema.type === "array" && typeof schema.items?.$ref === "string") {
    return `[]${goIdentifier(schemaNameFromReference(schema.items.$ref, "array item"))}`;
  }
  throw new Error(`unsupported Go schema type ${JSON.stringify(schema.type)}`);
}

function typescriptType(schema) {
  if (typeof schema.$ref === "string") {
    return schemaNameFromReference(schema.$ref, "field");
  }
  if (schema.type === "string") {
    return "string";
  }
  if (schema.type === "number" || schema.type === "integer") {
    return "number";
  }
  if (schema.type === "array" && typeof schema.items?.$ref === "string") {
    return `${schemaNameFromReference(schema.items.$ref, "array item")}[]`;
  }
  throw new Error(`unsupported TypeScript schema type ${JSON.stringify(schema.type)}`);
}
