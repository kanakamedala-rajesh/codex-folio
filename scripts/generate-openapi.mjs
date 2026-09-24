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
  const activitySourcesPath = `${activityPath}/sources`;
  const activityAssignmentsPath = `${activityPath}/assignments`;
  const alertsPath = `/api/${apiVersion}/alerts`;
  const analyticsPath = `/api/${apiVersion}/analytics`;
  const historyPath = `/api/${apiVersion}/analytics/history`;
  const handoffPath = `/api/${apiVersion}/handoff`;
  const bootstrapPath = `/api/${apiVersion}/bootstrap`;
  const browserTrustPath = `/api/${apiVersion}/browser-trust`;
  const collectionSettingsPath = `/api/${apiVersion}/collection-settings`;
  const diagnosticsPath = `/api/${apiVersion}/diagnostics`;
  const telemetryPath = `/api/${apiVersion}/telemetry`;
  const portableConfigurationPath = `/api/${apiVersion}/configuration`;
  const updatesPath = `/api/${apiVersion}/updates`;
  const metadataPath = `/api/${apiVersion}/meta`;
  const configurationPacksPath = `/api/${apiVersion}/configuration-packs`;
  const profileLifecyclePath = `/api/${apiVersion}/profile-lifecycle`;
  const profilesPath = `/api/${apiVersion}/profiles`;
  const projectsPath = `/api/${apiVersion}/projects`;
  const selectionPath = `/api/${apiVersion}/selection`;
  const usageLatestPath = `/api/${apiVersion}/usage/latest`;
  const usageRefreshPath = `/api/${apiVersion}/usage/refresh`;
  assertObject(contract.paths, "paths");
  assertExactKeys(
    contract.paths,
    [
      activityPath,
      activitySourcesPath,
      activityAssignmentsPath,
      alertsPath,
      analyticsPath,
      historyPath,
      bootstrapPath,
      browserTrustPath,
      collectionSettingsPath,
      diagnosticsPath,
      telemetryPath,
      portableConfigurationPath,
      updatesPath,
      configurationPacksPath,
      handoffPath,
      metadataPath,
      profileLifecyclePath,
      profilesPath,
      projectsPath,
      selectionPath,
      usageLatestPath,
      usageRefreshPath,
    ],
    "paths",
  );

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
  const browserTrustPathItem = contract.paths[browserTrustPath];
  assertObject(browserTrustPathItem, `path ${browserTrustPath}`);
  assertExactKeys(browserTrustPathItem, ["get", "post"], `path ${browserTrustPath}`);
  assertEqual(browserTrustPathItem.get.operationId, "getBrowserTrust", "browser trust GET operationId");
  assertEqual(browserTrustPathItem.post.operationId, "manageBrowserTrust", "browser trust POST operationId");
  assertEqual(requestReference(browserTrustPathItem.post.requestBody, "browser trust request"), "#/$defs/BrowserTrustRequest", "browser trust request");
  assertEqual(responseReference(browserTrustPathItem.get, "browser trust GET", ["200", "default"]), "#/$defs/BrowserTrustResponse", "browser trust GET response");
  assertEqual(responseReference(browserTrustPathItem.post, "browser trust POST", ["200", "default"]), "#/$defs/BrowserTrustResponse", "browser trust POST response");

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

  const collectionSettingsPathItem = contract.paths[collectionSettingsPath];
  assertObject(collectionSettingsPathItem, `path ${collectionSettingsPath}`);
  assertExactKeys(collectionSettingsPathItem, ["get", "put"], `path ${collectionSettingsPath}`);
  const getCollectionSettingsOperation = collectionSettingsPathItem.get;
  const setCollectionSettingsOperation = collectionSettingsPathItem.put;
  assertExactKeys(getCollectionSettingsOperation, ["operationId", "responses"], `GET ${collectionSettingsPath}`);
  assertExactKeys(setCollectionSettingsOperation, ["operationId", "requestBody", "responses"], `PUT ${collectionSettingsPath}`);
  assertIdentifier(getCollectionSettingsOperation.operationId, "get collection settings operationId");
  assertIdentifier(setCollectionSettingsOperation.operationId, "set collection settings operationId");
  const collectionSettingsRequestReference = requestReference(setCollectionSettingsOperation.requestBody, `PUT ${collectionSettingsPath} request body`);
  const collectionSettingsResponseReference = responseReference(getCollectionSettingsOperation, `GET ${collectionSettingsPath}`, ["200", "default"]);
  assertEqual(responseReference(setCollectionSettingsOperation, `PUT ${collectionSettingsPath}`, ["200", "default"]), collectionSettingsResponseReference, "collection settings response reference");

  const alertsPathItem = contract.paths[alertsPath];
  assertObject(alertsPathItem, `path ${alertsPath}`);
  assertExactKeys(alertsPathItem, ["get", "post"], `path ${alertsPath}`);
  const getAlertsOperation = alertsPathItem.get;
  const manageAlertsOperation = alertsPathItem.post;
  assertExactKeys(getAlertsOperation, ["operationId", "responses"], `GET ${alertsPath}`);
  assertExactKeys(manageAlertsOperation, ["operationId", "requestBody", "responses"], `POST ${alertsPath}`);
  assertIdentifier(getAlertsOperation.operationId, "get alerts operationId");
  assertIdentifier(manageAlertsOperation.operationId, "manage alerts operationId");
  const alertsResponseReference = responseReference(getAlertsOperation, `GET ${alertsPath}`, ["200", "default"]);
  const alertActionRequestReference = requestReference(manageAlertsOperation.requestBody, `POST ${alertsPath} request body`);
  assertEqual(responseReference(manageAlertsOperation, `POST ${alertsPath}`, ["200", "default"]), alertsResponseReference, "alerts response reference");
  assertEqual(errorResponseReference(getAlertsOperation, `GET ${alertsPath}`), "#/$defs/UsageErrorResponse", "alerts error response");
  assertEqual(errorResponseReference(manageAlertsOperation, `POST ${alertsPath}`), "#/$defs/UsageErrorResponse", "alerts mutation error response");

  const diagnosticsPathItem = contract.paths[diagnosticsPath];
  assertObject(diagnosticsPathItem, `path ${diagnosticsPath}`);
  assertExactKeys(diagnosticsPathItem, ["get", "post"], `path ${diagnosticsPath}`);
  const getDiagnosticsOperation = diagnosticsPathItem.get;
  const manageDiagnosticsOperation = diagnosticsPathItem.post;
  assertExactKeys(getDiagnosticsOperation, ["operationId", "responses"], `GET ${diagnosticsPath}`);
  assertExactKeys(manageDiagnosticsOperation, ["operationId", "requestBody", "responses"], `POST ${diagnosticsPath}`);
  assertIdentifier(getDiagnosticsOperation.operationId, "get diagnostics operationId");
  assertIdentifier(manageDiagnosticsOperation.operationId, "manage diagnostics operationId");
  const diagnosticsResponseReference = responseReference(getDiagnosticsOperation, `GET ${diagnosticsPath}`, ["200", "default"]);
  const diagnosticsRequestReference = requestReference(manageDiagnosticsOperation.requestBody, `POST ${diagnosticsPath} request body`);
  assertEqual(responseReference(manageDiagnosticsOperation, `POST ${diagnosticsPath}`, ["200", "default"]), diagnosticsResponseReference, "diagnostics response reference");
  assertEqual(errorResponseReference(getDiagnosticsOperation, `GET ${diagnosticsPath}`), "#/$defs/UsageErrorResponse", "diagnostics error response");
  assertEqual(errorResponseReference(manageDiagnosticsOperation, `POST ${diagnosticsPath}`), "#/$defs/UsageErrorResponse", "diagnostics mutation error response");

  const updatesPathItem = contract.paths[updatesPath];
  assertObject(updatesPathItem, `path ${updatesPath}`);
  assertExactKeys(updatesPathItem, ["get", "post"], `path ${updatesPath}`);
  const getUpdatesOperation = updatesPathItem.get;
  const manageUpdatesOperation = updatesPathItem.post;
  assertExactKeys(getUpdatesOperation, ["operationId", "responses"], `GET ${updatesPath}`);
  assertExactKeys(manageUpdatesOperation, ["operationId", "requestBody", "responses"], `POST ${updatesPath}`);
  assertIdentifier(getUpdatesOperation.operationId, "get updates operationId");
  assertIdentifier(manageUpdatesOperation.operationId, "manage updates operationId");
  const updatesResponseReference = responseReference(getUpdatesOperation, `GET ${updatesPath}`, ["200", "default"]);
  const updatesRequestReference = requestReference(manageUpdatesOperation.requestBody, `POST ${updatesPath} request body`);
  assertEqual(responseReference(manageUpdatesOperation, `POST ${updatesPath}`, ["200", "default"]), updatesResponseReference, "updates response reference");
  assertEqual(errorResponseReference(getUpdatesOperation, `GET ${updatesPath}`), "#/$defs/UsageErrorResponse", "updates error response");
  assertEqual(errorResponseReference(manageUpdatesOperation, `POST ${updatesPath}`), "#/$defs/UsageErrorResponse", "updates mutation error response");

  const telemetryPathItem = contract.paths[telemetryPath];
  assertObject(telemetryPathItem, `path ${telemetryPath}`);
  assertExactKeys(telemetryPathItem, ["get", "post"], `path ${telemetryPath}`);
  const getTelemetryOperation = telemetryPathItem.get;
  const manageTelemetryOperation = telemetryPathItem.post;
  assertExactKeys(getTelemetryOperation, ["operationId", "responses"], `GET ${telemetryPath}`);
  assertExactKeys(manageTelemetryOperation, ["operationId", "requestBody", "responses"], `POST ${telemetryPath}`);
  assertIdentifier(getTelemetryOperation.operationId, "get telemetry operationId");
  assertIdentifier(manageTelemetryOperation.operationId, "manage telemetry operationId");
  const telemetryResponseReference = responseReference(getTelemetryOperation, `GET ${telemetryPath}`, ["200", "default"]);
  const telemetryRequestReference = requestReference(manageTelemetryOperation.requestBody, `POST ${telemetryPath} request body`);
  assertEqual(responseReference(manageTelemetryOperation, `POST ${telemetryPath}`, ["200", "default"]), telemetryResponseReference, "telemetry response reference");
  assertEqual(errorResponseReference(getTelemetryOperation, `GET ${telemetryPath}`), "#/$defs/UsageErrorResponse", "telemetry error response");
  assertEqual(errorResponseReference(manageTelemetryOperation, `POST ${telemetryPath}`), "#/$defs/UsageErrorResponse", "telemetry mutation error response");

  const portableConfigurationPathItem = contract.paths[portableConfigurationPath];
  assertObject(portableConfigurationPathItem, `path ${portableConfigurationPath}`);
  assertExactKeys(portableConfigurationPathItem, ["get", "post"], `path ${portableConfigurationPath}`);
  const previewConfigurationExportOperation = portableConfigurationPathItem.get;
  const managePortableConfigurationOperation = portableConfigurationPathItem.post;
  assertExactKeys(previewConfigurationExportOperation, ["operationId", "responses"], `GET ${portableConfigurationPath}`);
  assertExactKeys(managePortableConfigurationOperation, ["operationId", "requestBody", "responses"], `POST ${portableConfigurationPath}`);
  const portableConfigurationResponseReference = responseReference(previewConfigurationExportOperation, `GET ${portableConfigurationPath}`, ["200", "default"]);
  const portableConfigurationRequestReference = requestReference(managePortableConfigurationOperation.requestBody, `POST ${portableConfigurationPath} request body`);
  assertEqual(responseReference(managePortableConfigurationOperation, `POST ${portableConfigurationPath}`, ["200", "default"]), portableConfigurationResponseReference, "portable configuration response reference");
  assertEqual(errorResponseReference(previewConfigurationExportOperation, `GET ${portableConfigurationPath}`), "#/$defs/UsageErrorResponse", "portable configuration error response");
  assertEqual(errorResponseReference(managePortableConfigurationOperation, `POST ${portableConfigurationPath}`), "#/$defs/UsageErrorResponse", "portable configuration mutation error response");

  const activityOperation = contract.paths[activityPath]?.get;
  assertObject(activityOperation, `GET ${activityPath}`);
  assertExactKeys(activityOperation, ["operationId", "parameters", "responses"], `GET ${activityPath}`);
  assertIdentifier(activityOperation.operationId, "activity operationId");
  if (!Array.isArray(activityOperation.parameters) || activityOperation.parameters.length !== 2) {
    throw new Error(`GET ${activityPath} must declare profile and project query parameters`);
  }
  const activityResponseReference = responseReference(activityOperation, `GET ${activityPath}`, ["200", "default"]);
  assertEqual(errorResponseReference(activityOperation, `GET ${activityPath}`), "#/$defs/UsageErrorResponse", "activity error response reference");
  const activitySourcesPathItem = contract.paths[activitySourcesPath];
  assertObject(activitySourcesPathItem, `path ${activitySourcesPath}`);
  assertExactKeys(activitySourcesPathItem, ["get", "post"], `path ${activitySourcesPath}`);
  const getActivitySourcesOperation = activitySourcesPathItem.get;
  const importActivitySourceOperation = activitySourcesPathItem.post;
  assertExactKeys(getActivitySourcesOperation, ["operationId", "responses"], `GET ${activitySourcesPath}`);
  assertExactKeys(importActivitySourceOperation, ["operationId", "requestBody", "responses"], `POST ${activitySourcesPath}`);
  assertEqual(getActivitySourcesOperation.operationId, "getActivitySources", "source review operationId");
  assertEqual(importActivitySourceOperation.operationId, "importActivitySource", "source import operationId");
  assertEqual(responseReference(getActivitySourcesOperation, `GET ${activitySourcesPath}`, ["200", "default"]), "#/$defs/ActivitySourcesResponse", "source review response");
  assertEqual(requestReference(importActivitySourceOperation.requestBody, `POST ${activitySourcesPath}`), "#/$defs/ActivitySourceImportRequest", "source import request");
  assertEqual(responseReference(importActivitySourceOperation, `POST ${activitySourcesPath}`, ["200", "default"]), "#/$defs/ActivitySourceImportResponse", "source import response");
  assertEqual(errorResponseReference(getActivitySourcesOperation, `GET ${activitySourcesPath}`), "#/$defs/UsageErrorResponse", "source review error response");
  assertEqual(errorResponseReference(importActivitySourceOperation, `POST ${activitySourcesPath}`), "#/$defs/UsageErrorResponse", "source import error response");
  const assignActivityOperation = contract.paths[activityAssignmentsPath]?.post;
  assertObject(assignActivityOperation, `POST ${activityAssignmentsPath}`);
  assertExactKeys(contract.paths[activityAssignmentsPath], ["post"], `path ${activityAssignmentsPath}`);
  assertExactKeys(assignActivityOperation, ["operationId", "requestBody", "responses"], `POST ${activityAssignmentsPath}`);
  assertEqual(assignActivityOperation.operationId, "assignActivity", "assignment operationId");
  assertEqual(requestReference(assignActivityOperation.requestBody, `POST ${activityAssignmentsPath}`), "#/$defs/ActivityAssignmentRequest", "assignment request");
  assertEqual(responseReference(assignActivityOperation, `POST ${activityAssignmentsPath}`, ["200", "default"]), "#/$defs/ActivityAssignmentResponse", "assignment response");
  assertEqual(errorResponseReference(assignActivityOperation, `POST ${activityAssignmentsPath}`), "#/$defs/UsageErrorResponse", "assignment error response");

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

  const projectsPathItem = contract.paths[projectsPath];
  assertObject(projectsPathItem, `path ${projectsPath}`);
  assertExactKeys(projectsPathItem, ["get", "put"], `path ${projectsPath}`);
  const getProjectsOperation = projectsPathItem.get;
  const editProjectOperation = projectsPathItem.put;
  assertExactKeys(getProjectsOperation, ["operationId", "responses"], `GET ${projectsPath}`);
  assertExactKeys(editProjectOperation, ["operationId", "requestBody", "responses"], `PUT ${projectsPath}`);
  assertIdentifier(getProjectsOperation.operationId, "get projects operationId");
  assertIdentifier(editProjectOperation.operationId, "edit project operationId");
  const projectEditRequestReference = requestReference(editProjectOperation.requestBody, `PUT ${projectsPath} request body`);
  const projectsResponseReference = responseReference(getProjectsOperation, `GET ${projectsPath}`, ["200", "default"]);
  assertEqual(errorResponseReference(getProjectsOperation, `GET ${projectsPath}`), "#/$defs/UsageErrorResponse", "projects error response reference");
  assertEqual(responseReference(editProjectOperation, `PUT ${projectsPath}`, ["200", "default"]), projectsResponseReference, "project edit response reference");
  assertEqual(errorResponseReference(editProjectOperation, `PUT ${projectsPath}`), "#/$defs/UsageErrorResponse", "project edit error response");

  const configurationPacksPathItem = contract.paths[configurationPacksPath];
  assertObject(configurationPacksPathItem, `path ${configurationPacksPath}`);
  assertExactKeys(configurationPacksPathItem, ["get", "post"], `path ${configurationPacksPath}`);
  const getConfigurationPacksOperation = configurationPacksPathItem.get;
  const manageConfigurationPackOperation = configurationPacksPathItem.post;
  assertExactKeys(getConfigurationPacksOperation, ["operationId", "responses"], `GET ${configurationPacksPath}`);
  assertExactKeys(manageConfigurationPackOperation, ["operationId", "requestBody", "responses"], `POST ${configurationPacksPath}`);
  assertIdentifier(getConfigurationPacksOperation.operationId, "get configuration packs operationId");
  assertIdentifier(manageConfigurationPackOperation.operationId, "manage configuration pack operationId");
  const configurationPackRequestReference = requestReference(manageConfigurationPackOperation.requestBody, `POST ${configurationPacksPath} request body`);
  const configurationPackResponseReference = responseReference(getConfigurationPacksOperation, `GET ${configurationPacksPath}`, ["200", "default"]);
  assertEqual(responseReference(manageConfigurationPackOperation, `POST ${configurationPacksPath}`, ["200", "default"]), configurationPackResponseReference, "configuration pack response reference");
  assertEqual(errorResponseReference(getConfigurationPacksOperation, `GET ${configurationPacksPath}`), "#/$defs/UsageErrorResponse", "configuration pack list error response");
  assertEqual(errorResponseReference(manageConfigurationPackOperation, `POST ${configurationPacksPath}`), "#/$defs/UsageErrorResponse", "configuration pack mutation error response");

  const profilesPathItem = contract.paths[profilesPath];
  assertObject(profilesPathItem, `path ${profilesPath}`);
  assertExactKeys(
    profilesPathItem,
    ["get", "post", "put"],
    `path ${profilesPath}`,
  );
  const getProfilesOperation = profilesPathItem.get;
  const editProfileOperation = profilesPathItem.put;
  const authenticateProfileOperation = profilesPathItem.post;
  assertExactKeys(
    getProfilesOperation,
    ["operationId", "responses"],
    `GET ${profilesPath}`,
  );
  assertExactKeys(
    editProfileOperation,
    ["operationId", "requestBody", "responses"],
    `PUT ${profilesPath}`,
  );
  assertExactKeys(
    authenticateProfileOperation,
    ["operationId", "requestBody", "responses"],
    `POST ${profilesPath}`,
  );
  assertIdentifier(
    getProfilesOperation.operationId,
    "get profiles operationId",
  );
  assertIdentifier(
    editProfileOperation.operationId,
    "edit profile operationId",
  );
  assertIdentifier(
    authenticateProfileOperation.operationId,
    "authenticate profile operationId",
  );
  const profilesResponseReference = responseReference(
    getProfilesOperation,
    `GET ${profilesPath}`,
    ["200", "default"],
  );
  assertEqual(
    responseReference(editProfileOperation, `PUT ${profilesPath}`, [
      "200",
      "default",
    ]),
    profilesResponseReference,
    "profiles response reference",
  );
  const profileEditRequestReference = requestReference(
    editProfileOperation.requestBody,
    `PUT ${profilesPath} request body`,
  );
  const profileAuthenticationRequestReference = requestReference(
    authenticateProfileOperation.requestBody,
    `POST ${profilesPath} request body`,
  );
  const profileAuthenticationResponseReference = responseReference(
    authenticateProfileOperation,
    `POST ${profilesPath}`,
    ["200", "default"],
  );

  const profileLifecyclePathItem = contract.paths[profileLifecyclePath];
  assertObject(profileLifecyclePathItem, `path ${profileLifecyclePath}`);
  assertExactKeys(profileLifecyclePathItem, ["get", "post"], `path ${profileLifecyclePath}`);
  const listProfileQuarantineOperation = profileLifecyclePathItem.get;
  const manageProfileLifecycleOperation = profileLifecyclePathItem.post;
  assertExactKeys(listProfileQuarantineOperation, ["operationId", "responses"], `GET ${profileLifecyclePath}`);
  assertExactKeys(manageProfileLifecycleOperation, ["operationId", "requestBody", "responses"], `POST ${profileLifecyclePath}`);
  assertIdentifier(listProfileQuarantineOperation.operationId, "list profile quarantine operationId");
  assertIdentifier(manageProfileLifecycleOperation.operationId, "manage profile lifecycle operationId");
  const profileLifecycleListResponseReference = responseReference(listProfileQuarantineOperation, `GET ${profileLifecyclePath}`, ["200", "default"]);
  const profileLifecycleRequestReference = requestReference(manageProfileLifecycleOperation.requestBody, `POST ${profileLifecyclePath} request body`);
  const profileLifecycleRecordReference = responseReference(manageProfileLifecycleOperation, `POST ${profileLifecyclePath}`, ["200", "default"]);
  assertEqual(errorResponseReference(listProfileQuarantineOperation, `GET ${profileLifecyclePath}`), "#/$defs/UsageErrorResponse", "profile lifecycle list error response");
  assertEqual(errorResponseReference(manageProfileLifecycleOperation, `POST ${profileLifecyclePath}`), "#/$defs/UsageErrorResponse", "profile lifecycle mutation error response");

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

  const historyOperation = contract.paths[historyPath]?.post;
  assertObject(historyOperation, `POST ${historyPath}`);
  assertExactKeys(contract.paths[historyPath], ["post"], `path ${historyPath}`);
  assertExactKeys(historyOperation, ["operationId", "requestBody", "responses"], `POST ${historyPath}`);
  assertEqual(historyOperation.operationId, "manageAnalyticsHistory", "history operationId");
  assertEqual(requestReference(historyOperation.requestBody, "history request body"), "#/$defs/HistoryRequest", "history request");
  assertEqual(responseReference(historyOperation, "history response", ["200", "default"]), "#/$defs/HistoryResponse", "history response");
  assertEqual(errorResponseReference(historyOperation, "history error"), usageErrorResponseReference, "history error response");
  const historySchemaNames = ["HistoryScope", "HistoryRequest", "HistoryResponse", "RetentionResult", "PurgeResult", "HistoryRecordCount", "HistoryMetric", "HistoryAggregate", "AnalyticsExportRequest", "AnalyticsExportDatasetPreview", "UsageExportRecord", "AvailabilityExportRecord", "AnalyticsExportRecords", "AnalyticsExportResult", "ActivityExportRecord", "ActivityCorrelation"];
  const handoffOperation = contract.paths[handoffPath]?.post;
  assertObject(handoffOperation, `POST ${handoffPath}`);
  assertExactKeys(contract.paths[handoffPath], ["post"], `path ${handoffPath}`);
  assertExactKeys(handoffOperation, ["operationId", "requestBody", "responses"], `POST ${handoffPath}`);
  assertEqual(handoffOperation.operationId, "manageHandoff", "handoff operationId");
  const handoffRequestReference = requestReference(handoffOperation.requestBody, "handoff request body");
  const handoffResponseReference = responseReference(handoffOperation, "handoff response", ["200", "default"]);
  assertEqual(errorResponseReference(handoffOperation, "handoff error"), usageErrorResponseReference, "handoff error response");
  const handoffSchemaNames = ["HandoffFields", "HandoffFieldEvidence", "HandoffValidationEvidence", "HandoffCheckpointFields", "HandoffRepository", "HandoffCheckpointSummary", "HandoffRetentionPolicy", "HandoffOperationPreview", "HandoffDownload", "HandoffResponse", "CheckpointManagementResponse", schemaNameFromReference(handoffRequestReference, "handoff request"), schemaNameFromReference(handoffResponseReference, "handoff response")];
  const configurationSchemaNames = ["ConfigurationDocument", "ConfigurationPackSummary", "ConfigurationChange", "ConfigurationAssignment", "ConfigurationProjectionPlan", "ConfigurationProjectionResult", "ConfigurationPromotionPreview", schemaNameFromReference(configurationPackRequestReference, "configuration pack request"), schemaNameFromReference(configurationPackResponseReference, "configuration pack response")];
  const alertSchemaNames = ["AlertRecord", "AlertThreshold", "AlertDeliveryHealth", schemaNameFromReference(alertsResponseReference, "alerts response"), schemaNameFromReference(alertActionRequestReference, "alert action request")];
  const diagnosticsSchemaNames = ["DiagnosticSettings", "DiagnosticFeatureStates", "DiagnosticHealth", "DiagnosticEnvironment", "DiagnosticRecord", "DiagnosticBundle", "DiagnosticPreview", schemaNameFromReference(diagnosticsRequestReference, "diagnostics request"), schemaNameFromReference(diagnosticsResponseReference, "diagnostics response")];
  const updateSchemaNames = [schemaNameFromReference(updatesRequestReference, "updates request"), schemaNameFromReference(updatesResponseReference, "updates response")];
  const telemetrySchemaNames = ["TelemetryPrerequisites", schemaNameFromReference(telemetryRequestReference, "telemetry request"), schemaNameFromReference(telemetryResponseReference, "telemetry response")];
  const portableConfigurationSchemaNames = ["PortableConfigurationProfile", "PortableConfigurationPack", "PortableConfigurationThreshold", "PortableConfigurationProjectAlias", "PortableConfigurationPreferences", "PortableConfigurationBundle", "PortableConfigurationCounts", "PortableConfigurationConflict", "PortableConfigurationPreview", "PortableConfigurationApplyResult", schemaNameFromReference(portableConfigurationRequestReference, "portable configuration request"), schemaNameFromReference(portableConfigurationResponseReference, "portable configuration response")];
  const schemaNames = [
    schemaNameFromReference(bootstrapRequestReference, "bootstrap request"),
    schemaNameFromReference(bootstrapResponseReference, "bootstrap response"),
    schemaNameFromReference(metadataResponseReference, "metadata response"),
    "ProfileSetupStages",
    "ProfileSummary",
    schemaNameFromReference(profilesResponseReference, "profiles response"),
    schemaNameFromReference(
      profileEditRequestReference,
      "profile edit request",
    ),
    schemaNameFromReference(
      profileAuthenticationRequestReference,
      "profile authentication request",
    ),
    schemaNameFromReference(
      profileAuthenticationResponseReference,
      "profile authentication response",
    ),
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
    "UsageCandidate",
    schemaNameFromReference(projectEditRequestReference, "project edit request"),
    ...historySchemaNames,
    ...handoffSchemaNames,
    ...configurationSchemaNames,
    ...alertSchemaNames,
    ...diagnosticsSchemaNames,
    ...updateSchemaNames,
    ...telemetrySchemaNames,
    ...portableConfigurationSchemaNames,
    schemaNameFromReference(profileLifecycleRecordReference, "profile lifecycle record"),
    schemaNameFromReference(profileLifecycleListResponseReference, "profile lifecycle list response"),
    schemaNameFromReference(profileLifecycleRequestReference, "profile lifecycle request"),
    schemaNameFromReference(collectionSettingsRequestReference, "collection settings request"),
    schemaNameFromReference(collectionSettingsResponseReference, "collection settings response"),
    "BrowserTrustRequest",
    "BrowserTrustResponse",
    "ActivitySource",
    "ActivitySourcesResponse",
    "ActivitySourceImportRequest",
    "ActivitySourceImportResponse",
    "ActivityAssignmentRequest",
    "ActivityAssignmentResponse",
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
  const metadataFields = schemaFields(
    contract.$defs[schemaNames[2]],
    schemaNames[2],
  );
  const profileSetupStagesFields = schemaFields(
    contract.$defs.ProfileSetupStages,
    "ProfileSetupStages",
  );
  const profileSummaryFields = schemaFields(
    contract.$defs.ProfileSummary,
    "ProfileSummary",
  );
  const profilesResponseFields = schemaFields(
    contract.$defs.ProfilesResponse,
    "ProfilesResponse",
  );
  const profileEditRequestFields = schemaFields(
    contract.$defs.ProfileEditRequest,
    "ProfileEditRequest",
  );
  const profileAuthenticationRequestFields = schemaFields(
    contract.$defs.ProfileAuthenticationRequest,
    "ProfileAuthenticationRequest",
  );
  const profileAuthenticationResponseFields = schemaFields(
    contract.$defs.ProfileAuthenticationResponse,
    "ProfileAuthenticationResponse",
  );
  const selectionRequestFields = schemaFields(
    contract.$defs[schemaNames[9]],
    schemaNames[9],
  );
  const selectionResponseFields = schemaFields(
    contract.$defs[schemaNames[10]],
    schemaNames[10],
  );
  const projectsResponseFields = schemaFields(
    contract.$defs[schemaNames[11]],
    schemaNames[11],
  );
  const usageRequestFields = schemaFields(
    contract.$defs[schemaNames[12]],
    schemaNames[12],
  );
  const usageResponseFields = schemaFields(
    contract.$defs[schemaNames[13]],
    schemaNames[13],
  );
  const usageErrorResponseFields = schemaFields(
    contract.$defs[schemaNames[14]],
    schemaNames[14],
  );
  const usageObservationFields = schemaFields(
    contract.$defs.UsageObservation,
    "UsageObservation",
  );
  const usageAvailabilityFields = schemaFields(
    contract.$defs.UsageMetricAvailability,
    "UsageMetricAvailability",
  );
  const projectIdentityFields = schemaFields(
    contract.$defs.ProjectIdentity,
    "ProjectIdentity",
  );
  const projectEditRequestFields = schemaFields(
    contract.$defs.ProjectEditRequest,
    "ProjectEditRequest",
  );
  const activityResponseFields = schemaFields(
    contract.$defs[schemaNames[15]],
    schemaNames[15],
  );
  const activityRecordFields = schemaFields(
    contract.$defs.ActivityRecord,
    "ActivityRecord",
  );
  const analyticsResponseFields = schemaFields(
    contract.$defs[schemaNames[20]],
    schemaNames[20],
  );
  const usageAggregateFields = schemaFields(
    contract.$defs.UsageAggregate,
    "UsageAggregate",
  );
  const usageMetricAmbiguityFields = schemaFields(
    contract.$defs.UsageMetricAmbiguity,
    "UsageMetricAmbiguity",
  );
  const usageCandidateFields = schemaFields(
    contract.$defs.UsageCandidate,
    "UsageCandidate",
  );
  const profileLifecycleRecordFields = schemaFields(contract.$defs.ProfileLifecycleRecord, "ProfileLifecycleRecord");
  const profileLifecycleListResponseFields = schemaFields(contract.$defs.ProfileLifecycleListResponse, "ProfileLifecycleListResponse");
  const profileLifecycleRequestFields = schemaFields(contract.$defs.ProfileLifecycleRequest, "ProfileLifecycleRequest");
  const collectionSettingsRequestType = schemaNameFromReference(collectionSettingsRequestReference, "collection settings request");
  const collectionSettingsResponseType = schemaNameFromReference(collectionSettingsResponseReference, "collection settings response");
  const collectionSettingsRequestFields = schemaFields(contract.$defs[collectionSettingsRequestType], collectionSettingsRequestType);
  const collectionSettingsResponseFields = schemaFields(contract.$defs[collectionSettingsResponseType], collectionSettingsResponseType);

  return {
    browserTrustPath,
    browserTrustRequestFields: schemaFields(contract.$defs.BrowserTrustRequest, "BrowserTrustRequest"),
    browserTrustResponseFields: schemaFields(contract.$defs.BrowserTrustResponse, "BrowserTrustResponse"),
    alertsPath,
    alertsGetOperationId: getAlertsOperation.operationId,
    alertsManageOperationId: manageAlertsOperation.operationId,
    alertsRequestType: schemaNameFromReference(alertActionRequestReference, "alert action request"),
    alertsResponseType: schemaNameFromReference(alertsResponseReference, "alerts response"),
    alertSchemas: alertSchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    diagnosticsPath,
    diagnosticsGetOperationId: getDiagnosticsOperation.operationId,
    diagnosticsManageOperationId: manageDiagnosticsOperation.operationId,
    diagnosticsRequestType: schemaNameFromReference(diagnosticsRequestReference, "diagnostics request"),
    diagnosticsResponseType: schemaNameFromReference(diagnosticsResponseReference, "diagnostics response"),
    diagnosticsSchemas: diagnosticsSchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    updatesPath,
    updatesGetOperationId: getUpdatesOperation.operationId,
    updatesManageOperationId: manageUpdatesOperation.operationId,
    updatesRequestType: schemaNameFromReference(updatesRequestReference, "updates request"),
    updatesResponseType: schemaNameFromReference(updatesResponseReference, "updates response"),
    updateSchemas: updateSchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    telemetryPath,
    telemetryGetOperationId: getTelemetryOperation.operationId,
    telemetryManageOperationId: manageTelemetryOperation.operationId,
    telemetryRequestType: schemaNameFromReference(telemetryRequestReference, "telemetry request"),
    telemetryResponseType: schemaNameFromReference(telemetryResponseReference, "telemetry response"),
    telemetrySchemas: telemetrySchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    portableConfigurationPath,
    portableConfigurationGetOperationId: previewConfigurationExportOperation.operationId,
    portableConfigurationManageOperationId: managePortableConfigurationOperation.operationId,
    portableConfigurationRequestType: schemaNameFromReference(portableConfigurationRequestReference, "portable configuration request"),
    portableConfigurationResponseType: schemaNameFromReference(portableConfigurationResponseReference, "portable configuration response"),
    portableConfigurationSchemas: portableConfigurationSchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    collectionSettingsPath,
    collectionSettingsGetOperationId: getCollectionSettingsOperation.operationId,
    collectionSettingsSetOperationId: setCollectionSettingsOperation.operationId,
    collectionSettingsRequestFields,
    collectionSettingsRequestType,
    collectionSettingsResponseFields,
    collectionSettingsResponseType,
    configurationPacksPath,
    configurationPackGetOperationId: getConfigurationPacksOperation.operationId,
    configurationPackManageOperationId: manageConfigurationPackOperation.operationId,
    configurationPackRequestType: schemaNameFromReference(configurationPackRequestReference, "configuration pack request"),
    configurationPackResponseType: schemaNameFromReference(configurationPackResponseReference, "configuration pack response"),
    configurationSchemas: configurationSchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    historyPath,
    historySchemas: historySchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    handoffPath,
    handoffOperationId: handoffOperation.operationId,
    handoffRequestType: schemaNameFromReference(handoffRequestReference, "handoff request"),
    handoffResponseType: schemaNameFromReference(handoffResponseReference, "handoff response"),
    handoffSchemas: handoffSchemaNames.map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    apiVersion,
    activityOperationId: activityOperation.operationId,
    activityPath,
    activitySourcesPath,
    activityAssignmentsPath,
    activitySourceSchemas: ["ActivitySource", "ActivitySourcesResponse", "ActivitySourceImportRequest", "ActivitySourceImportResponse", "ActivityAssignmentRequest", "ActivityAssignmentResponse"].map((name) => ({name, fields: schemaFields(contract.$defs[name], name)})),
    activityRecordFields,
    activityRecordType: "ActivityRecord",
    activityResponseFields,
    activityResponseType: schemaNames[15],
    analyticsOperationId: analyticsOperation.operationId,
    analyticsPath,
    analyticsResponseFields,
    analyticsResponseType: schemaNames[20],
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
    profilesPath,
    getProfilesOperationId: getProfilesOperation.operationId,
    editProfileOperationId: editProfileOperation.operationId,
    authenticateProfileOperationId: authenticateProfileOperation.operationId,
    profileSetupStagesFields,
    profileSummaryFields,
    profilesResponseFields,
    profileEditRequestFields,
    profileAuthenticationRequestFields,
    profileAuthenticationResponseFields,
    profileLifecyclePath,
    listProfileQuarantineOperationId: listProfileQuarantineOperation.operationId,
    manageProfileLifecycleOperationId: manageProfileLifecycleOperation.operationId,
    profileLifecycleRecordFields,
    profileLifecycleListResponseFields,
    profileLifecycleRequestFields,
    projectIdentityFields,
    projectIdentityType: "ProjectIdentity",
    projectEditOperationId: editProjectOperation.operationId,
    projectEditRequestFields,
    projectEditRequestType: schemaNameFromReference(projectEditRequestReference, "project edit request"),
    projectsOperationId: getProjectsOperation.operationId,
    projectsPath,
    projectsResponseFields,
    projectsResponseType: schemaNames[11],
    selectionGetOperationId: getSelectionOperation.operationId,
    selectionPath,
    selectionRequestFields,
    selectionRequestType: schemaNames[9],
    selectionResponseFields,
    selectionResponseType: schemaNames[10],
    selectionSetOperationId: setSelectionOperation.operationId,
    usageAvailabilityFields,
    usageAvailabilityType: "UsageMetricAvailability",
    usageAggregateFields,
    usageAggregateType: "UsageAggregate",
    usageMetricAmbiguityFields,
    usageMetricAmbiguityType: "UsageMetricAmbiguity",
    usageCandidateFields,
    usageObservationFields,
    usageObservationType: "UsageObservation",
    usageLatestOperationId: usageLatestOperation.operationId,
    usageLatestPath,
    usageOperationId: usageOperation.operationId,
    usageErrorResponseFields,
    usageErrorResponseType: schemaNames[14],
    usageRefreshPath,
    usageRequestFields,
    usageRequestType: schemaNames[12],
    usageResponseFields,
    usageResponseType: schemaNames[13],
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
    configurationPacksPath,
    configurationPackGetOperationId,
    configurationPackManageOperationId,
    configurationPackRequestType,
    configurationPackResponseType,
    apiVersion,
    activityOperationId,
    activityPath,
    activitySourcesPath,
    activityAssignmentsPath,
    activitySourceSchemas,
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
    collectionSettingsGetOperationId,
    collectionSettingsPath,
    collectionSettingsRequestFields,
    collectionSettingsRequestType,
    collectionSettingsResponseFields,
    collectionSettingsResponseType,
    collectionSettingsSetOperationId,
    handoffOperationId,
    handoffPath,
    handoffRequestType,
    handoffResponseType,
    metadataFields,
    metadataPath,
    metadataOperationId,
    metadataResponseType,
    profilesPath,
    getProfilesOperationId,
    editProfileOperationId,
    authenticateProfileOperationId,
    profileSetupStagesFields,
    profileSummaryFields,
    profilesResponseFields,
    profileEditRequestFields,
    profileAuthenticationRequestFields,
    profileAuthenticationResponseFields,
    profileLifecyclePath,
    listProfileQuarantineOperationId,
    manageProfileLifecycleOperationId,
    profileLifecycleRecordFields,
    profileLifecycleListResponseFields,
    profileLifecycleRequestFields,
    projectIdentityFields,
    projectIdentityType,
    projectEditOperationId,
    projectEditRequestFields,
    projectEditRequestType,
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
    usageCandidateFields,
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
  const configurationPackGetMethod = goIdentifier(configurationPackGetOperationId);
  const configurationPackManageMethod = goIdentifier(configurationPackManageOperationId);
  const analyticsMethod = goIdentifier(analyticsOperationId);
  const bootstrapMethod = goIdentifier(bootstrapOperationId);
  const metadataMethod = goIdentifier(metadataOperationId);
  const getProfilesMethod = goIdentifier(getProfilesOperationId);
  const editProfileMethod = goIdentifier(editProfileOperationId);
  const authenticateProfileMethod = goIdentifier(
    authenticateProfileOperationId,
  );
  const listProfileQuarantineMethod = goIdentifier(listProfileQuarantineOperationId);
  const manageProfileLifecycleMethod = goIdentifier(manageProfileLifecycleOperationId);
  const projectsMethod = goIdentifier(projectsOperationId);
  const editProjectMethod = goIdentifier(projectEditOperationId);
  const selectionGetMethod = goIdentifier(selectionGetOperationId);
  const selectionSetMethod = goIdentifier(selectionSetOperationId);
  const usageLatestMethod = goIdentifier(usageLatestOperationId);
  const usageMethod = goIdentifier(usageOperationId);
  const types = [
    ...contractShape.alertSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.diagnosticsSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.updateSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.telemetrySchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.portableConfigurationSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.configurationSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.historySchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...contractShape.handoffSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    ...activitySourceSchemas.map(({name, fields}) => renderGoStruct(name, fields)),
    renderGoStruct(activityRecordType, activityRecordFields),
    renderGoStruct(activityResponseType, activityResponseFields),
    renderGoStruct(analyticsResponseType, analyticsResponseFields),
    renderGoStruct(bootstrapRequestType, bootstrapRequestFields),
    renderGoStruct(bootstrapResponseType, bootstrapResponseFields),
    renderGoStruct("BrowserTrustRequest", contractShape.browserTrustRequestFields),
    renderGoStruct("BrowserTrustResponse", contractShape.browserTrustResponseFields),
    renderGoStruct(collectionSettingsRequestType, collectionSettingsRequestFields),
    renderGoStruct(collectionSettingsResponseType, collectionSettingsResponseFields),
    renderGoStruct(metadataResponseType, metadataFields),
    renderGoStruct("ProfileSetupStages", profileSetupStagesFields),
    renderGoStruct("ProfileSummary", profileSummaryFields),
    renderGoStruct("ProfilesResponse", profilesResponseFields),
    renderGoStruct("ProfileEditRequest", profileEditRequestFields),
    renderGoStruct(
      "ProfileAuthenticationRequest",
      profileAuthenticationRequestFields,
    ),
    renderGoStruct(
      "ProfileAuthenticationResponse",
      profileAuthenticationResponseFields,
    ),
    renderGoStruct("ProfileLifecycleRecord", profileLifecycleRecordFields),
    renderGoStruct("ProfileLifecycleListResponse", profileLifecycleListResponseFields),
    renderGoStruct("ProfileLifecycleRequest", profileLifecycleRequestFields),
    renderGoStruct(projectIdentityType, projectIdentityFields),
    renderGoStruct(projectEditRequestType, projectEditRequestFields),
    renderGoStruct(projectsResponseType, projectsResponseFields),
    renderGoStruct(selectionRequestType, selectionRequestFields),
    renderGoStruct(selectionResponseType, selectionResponseFields),
    renderGoStruct(usageRequestType, usageRequestFields),
    renderGoStruct(usageErrorResponseType, usageErrorResponseFields),
    renderGoStruct(usageObservationType, usageObservationFields),
    renderGoStruct(usageAvailabilityType, usageAvailabilityFields),
    renderGoStruct(usageAggregateType, usageAggregateFields),
    renderGoStruct(usageMetricAmbiguityType, usageMetricAmbiguityFields),
    renderGoStruct("UsageCandidate", usageCandidateFields),
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
\tActivitySourcesPath  = "${activitySourcesPath}"
\tActivityAssignmentsPath = "${activityAssignmentsPath}"
\tAlertsPath           = "${contractShape.alertsPath}"
\tAnalyticsPath        = "${analyticsPath}"
\tHistoryPath          = "${contractShape.historyPath}"
\tHandoffPath          = "${contractShape.handoffPath}"
\tContractVersion      = "${productVersion}"
\tContractSourceSHA256 = "${sourceHash}"
\tBootstrapPath        = "${bootstrapPath}"
\tBrowserTrustPath     = "${contractShape.browserTrustPath}"
\tCollectionSettingsPath = "${collectionSettingsPath}"
\tDiagnosticsPath      = "${contractShape.diagnosticsPath}"
\tUpdatesPath          = "${contractShape.updatesPath}"
\tTelemetryPath        = "${contractShape.telemetryPath}"
\tPortableConfigurationPath = "${contractShape.portableConfigurationPath}"
\tConfigurationPacksPath = "${configurationPacksPath}"
\tMetadataPath         = "${metadataPath}"
\tProfileLifecyclePath = "${profileLifecyclePath}"
\tProfilesPath         = "${profilesPath}"
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

func (client *Client) ${contractShape.diagnosticsGetOperationId}(ctx context.Context) (${contractShape.diagnosticsResponseType}, *http.Response, error) {
\tvar result ${contractShape.diagnosticsResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+DiagnosticsPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.diagnosticsManageOperationId}(ctx context.Context, input ${contractShape.diagnosticsRequestType}) (${contractShape.diagnosticsResponseType}, *http.Response, error) {
\tvar result ${contractShape.diagnosticsResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+DiagnosticsPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.updatesGetOperationId}(ctx context.Context) (${contractShape.updatesResponseType}, *http.Response, error) {
\tvar result ${contractShape.updatesResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+UpdatesPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.updatesManageOperationId}(ctx context.Context, input ${contractShape.updatesRequestType}) (${contractShape.updatesResponseType}, *http.Response, error) {
\tvar result ${contractShape.updatesResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+UpdatesPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.telemetryGetOperationId}(ctx context.Context) (${contractShape.telemetryResponseType}, *http.Response, error) {
\tvar result ${contractShape.telemetryResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+TelemetryPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.telemetryManageOperationId}(ctx context.Context, input ${contractShape.telemetryRequestType}) (${contractShape.telemetryResponseType}, *http.Response, error) {
\tvar result ${contractShape.telemetryResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+TelemetryPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.portableConfigurationGetOperationId}(ctx context.Context) (${contractShape.portableConfigurationResponseType}, *http.Response, error) {
\tvar result ${contractShape.portableConfigurationResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+PortableConfigurationPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${contractShape.portableConfigurationManageOperationId}(ctx context.Context, input ${contractShape.portableConfigurationRequestType}) (${contractShape.portableConfigurationResponseType}, *http.Response, error) {
\tvar result ${contractShape.portableConfigurationResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+PortableConfigurationPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ManageAnalyticsHistory(ctx context.Context, input HistoryRequest) (HistoryResponse, *http.Response, error) {
\tvar result HistoryResponse
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+HistoryPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${configurationPackGetMethod}(ctx context.Context) (${configurationPackResponseType}, *http.Response, error) {
\tvar result ${configurationPackResponseType}
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+ConfigurationPacksPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${configurationPackManageMethod}(ctx context.Context, input ${configurationPackRequestType}) (${configurationPackResponseType}, *http.Response, error) {
\tvar result ${configurationPackResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+ConfigurationPacksPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
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

func (client *Client) GetActivitySources(ctx context.Context) (ActivitySourcesResponse, *http.Response, error) {
\tvar result ActivitySourcesResponse
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+ActivitySourcesPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf("GET %s returned HTTP %d", ActivitySourcesPath, response.StatusCode)
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ImportActivitySource(ctx context.Context, input ActivitySourceImportRequest) (ActivitySourceImportResponse, *http.Response, error) {
\tvar result ActivitySourceImportResponse
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+ActivitySourcesPath, bytes.NewReader(body))
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\trequest.Header.Set("Content-Type", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\treturn result, response, fmt.Errorf("POST %s returned HTTP %d", ActivitySourcesPath, response.StatusCode)
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
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

func (client *Client) ${editProjectMethod}(ctx context.Context, input ${projectEditRequestType}) (${projectsResponseType}, *http.Response, error) {
\tvar result ${projectsResponseType}
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPut, client.baseURL+ProjectsPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${getProfilesMethod}(ctx context.Context) (ProfilesResponse, *http.Response, error) {
\tvar result ProfilesResponse
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+ProfilesPath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${editProfileMethod}(ctx context.Context, input ProfileEditRequest) (ProfilesResponse, *http.Response, error) {
\tvar result ProfilesResponse
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPut, client.baseURL+ProfilesPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${authenticateProfileMethod}(ctx context.Context, input ProfileAuthenticationRequest) (ProfileAuthenticationResponse, *http.Response, error) {
\tvar result ProfileAuthenticationResponse
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+ProfilesPath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${listProfileQuarantineMethod}(ctx context.Context) (ProfileLifecycleListResponse, *http.Response, error) {
\tvar result ProfileLifecycleListResponse
\trequest, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+ProfileLifecyclePath, nil)
\tif err != nil { return result, nil, err }
\trequest.Header.Set("Accept", "application/json")
\thttpClient := client.httpClient
\tif httpClient == nil { httpClient = http.DefaultClient }
\tresponse, err := httpClient.Do(request)
\tif err != nil { return result, nil, err }
\tdefer response.Body.Close()
\tif response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
\t\tvar failure ${usageErrorResponseType}
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
}

func (client *Client) ${manageProfileLifecycleMethod}(ctx context.Context, input ProfileLifecycleRequest) (ProfileLifecycleRecord, *http.Response, error) {
\tvar result ProfileLifecycleRecord
\tbody, err := json.Marshal(input)
\tif err != nil { return result, nil, err }
\trequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+ProfileLifecyclePath, bytes.NewReader(body))
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
\t\tif err := json.NewDecoder(response.Body).Decode(&failure); err != nil { return result, response, err }
\t\treturn result, response, failure
\t}
\terr = json.NewDecoder(response.Body).Decode(&result)
\treturn result, response, err
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
    handoffOperationId,
    handoffPath,
    handoffRequestType,
    handoffResponseType,
    configurationPacksPath,
    configurationPackGetOperationId,
    configurationPackManageOperationId,
    configurationPackRequestType,
    configurationPackResponseType,
    apiVersion,
    activityOperationId,
    activityPath,
    activitySourcesPath,
    activityAssignmentsPath,
    activitySourceSchemas,
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
    collectionSettingsGetOperationId,
    collectionSettingsPath,
    collectionSettingsRequestFields,
    collectionSettingsRequestType,
    collectionSettingsResponseFields,
    collectionSettingsResponseType,
    collectionSettingsSetOperationId,
    metadataFields,
    metadataPath,
    metadataOperationId,
    metadataResponseType,
    profilesPath,
    getProfilesOperationId,
    editProfileOperationId,
    authenticateProfileOperationId,
    profileSetupStagesFields,
    profileSummaryFields,
    profilesResponseFields,
    profileEditRequestFields,
    profileAuthenticationRequestFields,
    profileAuthenticationResponseFields,
    profileLifecyclePath,
    listProfileQuarantineOperationId,
    manageProfileLifecycleOperationId,
    profileLifecycleRecordFields,
    profileLifecycleListResponseFields,
    profileLifecycleRequestFields,
    projectIdentityFields,
    projectIdentityType,
    projectEditOperationId,
    projectEditRequestFields,
    projectEditRequestType,
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
    usageCandidateFields,
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
  const activityRecordLines = activityRecordFields.map(({ name, required, schema }) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n");
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
  const collectionSettingsRequestLines = collectionSettingsRequestFields
    .map(({ name, required, schema }) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`)
    .join("\n");
  const collectionSettingsResponseLines = collectionSettingsResponseFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const profileSetupStagesLines = profileSetupStagesFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const profileSummaryLines = profileSummaryFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const profilesResponseLines = profilesResponseFields
    .map(
      ({ name, required, schema }) =>
        `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`,
    )
    .join("\n");
  const profileEditRequestLines = profileEditRequestFields
    .map(
      ({ name, required, schema }) =>
        `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`,
    )
    .join("\n");
  const profileAuthenticationRequestLines = profileAuthenticationRequestFields
    .map(
      ({ name, required, schema }) =>
        `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`,
    )
    .join("\n");
  const profileAuthenticationResponseLines = profileAuthenticationResponseFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const profileLifecycleRecordLines = profileLifecycleRecordFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const profileLifecycleListResponseLines = profileLifecycleListResponseFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const profileLifecycleRequestLines = profileLifecycleRequestFields
    .map(({ name, required, schema }) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`)
    .join("\n");
  const projectIdentityLines = projectIdentityFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const projectEditRequestLines = projectEditRequestFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
  const projectsResponseLines = projectsResponseFields
    .map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`)
    .join("\n");
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
  const usageCandidateLines = usageCandidateFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");
  const usageResponseLines = usageResponseFields.map(({ name, schema }) => `  ${name}: ${typescriptType(schema)};`).join("\n");

  return `// Code generated by codex-folio OpenAPI generator ${GENERATOR_VERSION}; DO NOT EDIT.
// Contract source: ${contractPath}
export const API_VERSION = "${apiVersion}" as const;
export const CONTRACT_VERSION = "${productVersion}" as const;
export const CONTRACT_SOURCE_SHA256 =
  "${sourceHash}" as const;
export const HandoffPath = "${handoffPath}" as const;
export const AlertsPath = "${contractShape.alertsPath}" as const;
export const DiagnosticsPath = "${contractShape.diagnosticsPath}" as const;
export const UpdatesPath = "${contractShape.updatesPath}" as const;
export const TelemetryPath = "${contractShape.telemetryPath}" as const;
export const PortableConfigurationPath = "${contractShape.portableConfigurationPath}" as const;
export const BrowserTrustPath = "${contractShape.browserTrustPath}" as const;
export const BootstrapPath = "${bootstrapPath}" as const;

export interface BrowserTrustRequest {
${contractShape.browserTrustRequestFields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}
}

export interface BrowserTrustResponse {
${contractShape.browserTrustResponseFields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}
}

${contractShape.alertSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.diagnosticsSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.updateSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.telemetrySchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.portableConfigurationSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.historySchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.handoffSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${contractShape.configurationSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

${activitySourceSchemas.map(({name, fields}) => `export interface ${name} {\n${fields.map(({name, required, schema}) => `  ${name}${required ? "" : "?"}: ${typescriptType(schema)};`).join("\n")}\n}`).join("\n\n")}

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

export interface ${collectionSettingsRequestType} {
${collectionSettingsRequestLines}
}

export interface ${collectionSettingsResponseType} {
${collectionSettingsResponseLines}
}

export interface ProfileSetupStages {
${profileSetupStagesLines}
}

export interface ProfileSummary {
${profileSummaryLines}
}

export interface ProfilesResponse {
${profilesResponseLines}
}

export interface ProfileEditRequest {
${profileEditRequestLines}
}

export interface ProfileAuthenticationRequest {
${profileAuthenticationRequestLines}
}

export interface ProfileAuthenticationResponse {
${profileAuthenticationResponseLines}
}

export interface ProfileLifecycleRecord {
${profileLifecycleRecordLines}
}

export interface ProfileLifecycleListResponse {
${profileLifecycleListResponseLines}
}

export interface ProfileLifecycleRequest {
${profileLifecycleRequestLines}
}

export interface ${projectIdentityType} {
${projectIdentityLines}
}

export interface ${projectEditRequestType} {
${projectEditRequestLines}
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

export interface UsageCandidate {
${usageCandidateLines}
}

export interface ${usageResponseType} {
${usageResponseLines}
}

export interface ApiPaths {
  "${contractShape.portableConfigurationPath}": {
    get: {
      operationId: "${contractShape.portableConfigurationGetOperationId}";
      responses: {
        200: { content: { "application/json": ${contractShape.portableConfigurationResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${contractShape.portableConfigurationManageOperationId}";
      requestBody: ${contractShape.portableConfigurationRequestType};
      responses: {
        200: { content: { "application/json": ${contractShape.portableConfigurationResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${contractShape.telemetryPath}": {
    get: {
      operationId: "${contractShape.telemetryGetOperationId}";
      responses: {
        200: { content: { "application/json": ${contractShape.telemetryResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${contractShape.telemetryManageOperationId}";
      requestBody: ${contractShape.telemetryRequestType};
      responses: {
        200: { content: { "application/json": ${contractShape.telemetryResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${contractShape.updatesPath}": {
    get: {
      operationId: "${contractShape.updatesGetOperationId}";
      responses: {
        200: { content: { "application/json": ${contractShape.updatesResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${contractShape.updatesManageOperationId}";
      requestBody: ${contractShape.updatesRequestType};
      responses: {
        200: { content: { "application/json": ${contractShape.updatesResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${contractShape.diagnosticsPath}": {
    get: {
      operationId: "${contractShape.diagnosticsGetOperationId}";
      responses: {
        200: { content: { "application/json": ${contractShape.diagnosticsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${contractShape.diagnosticsManageOperationId}";
      requestBody: ${contractShape.diagnosticsRequestType};
      responses: {
        200: { content: { "application/json": ${contractShape.diagnosticsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${contractShape.alertsPath}": {
    get: {
      operationId: "${contractShape.alertsGetOperationId}";
      responses: {
        200: { content: { "application/json": ${contractShape.alertsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${contractShape.alertsManageOperationId}";
      requestBody: ${contractShape.alertsRequestType};
      responses: {
        200: { content: { "application/json": ${contractShape.alertsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${configurationPacksPath}": {
    get: {
      operationId: "${configurationPackGetOperationId}";
      responses: {
        200: { content: { "application/json": ${configurationPackResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${configurationPackManageOperationId}";
      requestBody: ${configurationPackRequestType};
      responses: {
        200: { content: { "application/json": ${configurationPackResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${contractShape.historyPath}": {
    post: {
      operationId: "manageAnalyticsHistory";
      requestBody: HistoryRequest;
      responses: {
        200: { content: { "application/json": HistoryResponse } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${handoffPath}": {
    post: {
      operationId: "${handoffOperationId}";
      requestBody: ${handoffRequestType};
      responses: {
        200: { content: { "application/json": ${handoffResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
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
  "${activitySourcesPath}": {
    get: {
      operationId: "getActivitySources";
      responses: { 200: { content: { "application/json": ActivitySourcesResponse } } };
    };
    post: {
      operationId: "importActivitySource";
      requestBody: ActivitySourceImportRequest;
      responses: { 200: { content: { "application/json": ActivitySourceImportResponse } } };
    };
  };
  "${activityAssignmentsPath}": {
    post: {
      operationId: "assignActivity";
      requestBody: ActivityAssignmentRequest;
      responses: { 200: { content: { "application/json": ActivityAssignmentResponse } } };
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
  "${contractShape.browserTrustPath}": {
    get: {
      operationId: "getBrowserTrust";
      responses: { 200: { content: { "application/json": BrowserTrustResponse } } };
    };
    post: {
      operationId: "manageBrowserTrust";
      requestBody: BrowserTrustRequest;
      responses: { 200: { content: { "application/json": BrowserTrustResponse } } };
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
  "${collectionSettingsPath}": {
    get: {
      operationId: "${collectionSettingsGetOperationId}";
      responses: {
        200: { content: { "application/json": ${collectionSettingsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    put: {
      operationId: "${collectionSettingsSetOperationId}";
      requestBody: ${collectionSettingsRequestType};
      responses: {
        200: { content: { "application/json": ${collectionSettingsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${profilesPath}": {
    get: {
      operationId: "${getProfilesOperationId}";
      responses: {
        200: { content: { "application/json": ProfilesResponse } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    put: {
      operationId: "${editProfileOperationId}";
      requestBody: ProfileEditRequest;
      responses: {
        200: { content: { "application/json": ProfilesResponse } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${authenticateProfileOperationId}";
      requestBody: ProfileAuthenticationRequest;
      responses: {
        200: { content: { "application/json": ProfileAuthenticationResponse } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
  };
  "${profileLifecyclePath}": {
    get: {
      operationId: "${listProfileQuarantineOperationId}";
      responses: {
        200: { content: { "application/json": ProfileLifecycleListResponse } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
    };
    post: {
      operationId: "${manageProfileLifecycleOperationId}";
      requestBody: ProfileLifecycleRequest;
      responses: {
        200: { content: { "application/json": ProfileLifecycleRecord } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
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
    put: {
      operationId: "${projectEditOperationId}";
      requestBody: ${projectEditRequestType};
      responses: {
        200: { content: { "application/json": ${projectsResponseType} } };
        default: { content: { "application/json": ${usageErrorResponseType} } };
      };
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
  ${contractShape.portableConfigurationGetOperationId}(init?: RequestInit): Promise<${contractShape.portableConfigurationResponseType}>;
  ${contractShape.portableConfigurationManageOperationId}(
    request: ${contractShape.portableConfigurationRequestType},
    init?: RequestInit,
  ): Promise<${contractShape.portableConfigurationResponseType}>;
  ${contractShape.telemetryGetOperationId}(init?: RequestInit): Promise<${contractShape.telemetryResponseType}>;
  ${contractShape.telemetryManageOperationId}(request: ${contractShape.telemetryRequestType}, init?: RequestInit): Promise<${contractShape.telemetryResponseType}>;
  ${contractShape.updatesGetOperationId}(init?: RequestInit): Promise<${contractShape.updatesResponseType}>;
  ${contractShape.updatesManageOperationId}(request: ${contractShape.updatesRequestType}, init?: RequestInit): Promise<${contractShape.updatesResponseType}>;
  ${contractShape.diagnosticsGetOperationId}(init?: RequestInit): Promise<${contractShape.diagnosticsResponseType}>;
  ${contractShape.diagnosticsManageOperationId}(request: ${contractShape.diagnosticsRequestType}, init?: RequestInit): Promise<${contractShape.diagnosticsResponseType}>;
  ${contractShape.alertsGetOperationId}(init?: RequestInit): Promise<${contractShape.alertsResponseType}>;
  ${contractShape.alertsManageOperationId}(request: ${contractShape.alertsRequestType}, init?: RequestInit): Promise<${contractShape.alertsResponseType}>;
  ${configurationPackGetOperationId}(init?: RequestInit): Promise<${configurationPackResponseType}>;
  ${configurationPackManageOperationId}(
    request: ${configurationPackRequestType},
    init?: RequestInit,
  ): Promise<${configurationPackResponseType}>;
  manageAnalyticsHistory(request: HistoryRequest, init?: RequestInit): Promise<HistoryResponse>;
  ${handoffOperationId}(request: ${handoffRequestType}, init?: RequestInit): Promise<${handoffResponseType}>;
  ${analyticsOperationId}(scope?: string, init?: RequestInit): Promise<${analyticsResponseType}>;
  ${activityOperationId}(
    profileAlias?: string,
    projectId?: string,
    init?: RequestInit,
  ): Promise<${activityResponseType}>;
  getActivitySources(init?: RequestInit): Promise<ActivitySourcesResponse>;
  importActivitySource(
    request: ActivitySourceImportRequest,
    init?: RequestInit,
  ): Promise<ActivitySourceImportResponse>;
  assignActivity(
    request: ActivityAssignmentRequest,
    init?: RequestInit,
  ): Promise<ActivityAssignmentResponse>;
  ${bootstrapOperationId}(request: ${bootstrapRequestType}, init?: RequestInit): Promise<${bootstrapResponseType}>;
  getBrowserTrust(init?: RequestInit): Promise<BrowserTrustResponse>;
  manageBrowserTrust(
    request: BrowserTrustRequest,
    init?: RequestInit,
  ): Promise<BrowserTrustResponse>;
  ${metadataOperationId}(init?: RequestInit): Promise<${metadataResponseType}>;
  ${collectionSettingsGetOperationId}(init?: RequestInit): Promise<${collectionSettingsResponseType}>;
  ${collectionSettingsSetOperationId}(
    request: ${collectionSettingsRequestType},
    init?: RequestInit,
  ): Promise<${collectionSettingsResponseType}>;
  ${getProfilesOperationId}(init?: RequestInit): Promise<ProfilesResponse>;
  ${editProfileOperationId}(request: ProfileEditRequest, init?: RequestInit): Promise<ProfilesResponse>;
  ${authenticateProfileOperationId}(
    request: ProfileAuthenticationRequest,
    init?: RequestInit,
  ): Promise<ProfileAuthenticationResponse>;
  ${listProfileQuarantineOperationId}(init?: RequestInit): Promise<ProfileLifecycleListResponse>;
  ${manageProfileLifecycleOperationId}(
    request: ProfileLifecycleRequest,
    init?: RequestInit,
  ): Promise<ProfileLifecycleRecord>;
  ${projectsOperationId}(init?: RequestInit): Promise<${projectsResponseType}>;
  ${projectEditOperationId}(request: ${projectEditRequestType}, init?: RequestInit): Promise<${projectsResponseType}>;
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
    async ${contractShape.portableConfigurationGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.portableConfigurationPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${contractShape.portableConfigurationResponseType};
    },
    async ${contractShape.portableConfigurationManageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.portableConfigurationPath}", {
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
      return (await response.json()) as ${contractShape.portableConfigurationResponseType};
    },
    async ${contractShape.telemetryGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.telemetryPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${contractShape.telemetryResponseType};
    },
    async ${contractShape.telemetryManageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.telemetryPath}", {
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
      return (await response.json()) as ${contractShape.telemetryResponseType};
    },
    async ${contractShape.updatesGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.updatesPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${contractShape.updatesResponseType};
    },
    async ${contractShape.updatesManageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.updatesPath}", {
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
      return (await response.json()) as ${contractShape.updatesResponseType};
    },
    async ${contractShape.diagnosticsGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.diagnosticsPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${contractShape.diagnosticsResponseType};
    },
    async ${contractShape.diagnosticsManageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.diagnosticsPath}", {
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
      return (await response.json()) as ${contractShape.diagnosticsResponseType};
    },
    async ${contractShape.alertsGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.alertsPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${contractShape.alertsResponseType};
    },
    async ${contractShape.alertsManageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.alertsPath}", {
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
      return (await response.json()) as ${contractShape.alertsResponseType};
    },
    async ${configurationPackGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${configurationPacksPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${configurationPackResponseType};
    },
    async ${configurationPackManageOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${configurationPacksPath}", {
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
      return (await response.json()) as ${configurationPackResponseType};
    },
    async manageAnalyticsHistory(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${contractShape.historyPath}", {
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
      return (await response.json()) as HistoryResponse;
    },
    async ${handoffOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${handoffPath}", {
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
      return (await response.json()) as ${handoffResponseType};
    },
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
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${activityResponseType};
    },
    async getActivitySources(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${activitySourcesPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ActivitySourcesResponse;
    },
    async importActivitySource(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${activitySourcesPath}", {
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
      return (await response.json()) as ActivitySourceImportResponse;
    },
    async assignActivity(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${activityAssignmentsPath}", {
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
      return (await response.json()) as ActivityAssignmentResponse;
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
    async getBrowserTrust(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + BrowserTrustPath, {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as BrowserTrustResponse;
    },
    async manageBrowserTrust(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + BrowserTrustPath, {
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
      return (await response.json()) as BrowserTrustResponse;
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
    async ${collectionSettingsGetOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${collectionSettingsPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${collectionSettingsResponseType};
    },
    async ${collectionSettingsSetOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${collectionSettingsPath}", {
        ...init,
        body: JSON.stringify(request),
        credentials: init.credentials ?? "include",
        headers,
        method: "PUT",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${collectionSettingsResponseType};
    },
    async ${getProfilesOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${profilesPath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ProfilesResponse;
    },
    async ${editProfileOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${profilesPath}", {
        ...init,
        body: JSON.stringify(request),
        credentials: init.credentials ?? "include",
        headers,
        method: "PUT",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ProfilesResponse;
    },
    async ${authenticateProfileOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${profilesPath}", {
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
      return (await response.json()) as ProfileAuthenticationResponse;
    },
    async ${listProfileQuarantineOperationId}(init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      const response = await fetcher(baseUrl + "${profileLifecyclePath}", {
        ...init,
        credentials: init.credentials ?? "include",
        headers,
        method: "GET",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ProfileLifecycleListResponse;
    },
    async ${manageProfileLifecycleOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${profileLifecyclePath}", {
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
      return (await response.json()) as ProfileLifecycleRecord;
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
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
      }
      return (await response.json()) as ${projectsResponseType};
    },
    async ${projectEditOperationId}(request, init = {}) {
      const headers = new Headers(init.headers);
      headers.set("Accept", "application/json");
      headers.set("Content-Type", "application/json");
      const response = await fetcher(baseUrl + "${projectsPath}", {
        ...init,
        body: JSON.stringify(request),
        credentials: init.credentials ?? "include",
        headers,
        method: "PUT",
      });
      if (!response.ok) {
        const failure = (await response.json()) as ${usageErrorResponseType};
        throw new UsageRefreshError(failure.code, response.status, failure.message);
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
  if (schema.type === "array" && schema.items?.type === "string") return "[]string";
  if (schema.type === "boolean") return "bool";
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
  if (schema.type === "object" && schema.additionalProperties?.type === "string") return "map[string]string";
  throw new Error(`unsupported Go schema type ${JSON.stringify(schema.type)}`);
}

function typescriptType(schema) {
  if (schema.type === "array" && schema.items?.type === "string") return "string[]";
  if (schema.type === "boolean") return "boolean";
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
  if (schema.type === "object" && schema.additionalProperties?.type === "string") return "Record<string, string>";
  throw new Error(`unsupported TypeScript schema type ${JSON.stringify(schema.type)}`);
}
