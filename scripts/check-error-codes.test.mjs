import assert from "node:assert/strict";
import test from "node:test";

import {
  errorCodeFailures,
  gitPath,
  goConstantFailures,
  historicalRetiredCodeFailures,
} from "./check-error-codes.mjs";

const validRegistry = {
  version: 1,
  codes: [
    {
      code: "CF_CLI_USAGE",
      owner: "cli",
      lifecycle: "active",
      introduced_in: "0.0.1-alpha",
    },
  ],
};

test("Git object paths use forward slashes on every host", () => {
  assert.equal(
    gitPath("internal\\apperrors\\codes.json"),
    "internal/apperrors/codes.json",
  );
  assert.equal(
    gitPath("internal/apperrors/codes.json"),
    "internal/apperrors/codes.json",
  );
});

test("a well-formed registry is accepted", () => {
  assert.deepEqual(errorCodeFailures(validRegistry), []);
});

test("duplicate and retired identifiers cannot be reused", () => {
  const registry = structuredClone(validRegistry);
  registry.codes.push({
    code: "CF_CLI_USAGE",
    owner: "cli",
    lifecycle: "retired",
    introduced_in: "0.0.1-alpha",
    retired_in: "1.0.0",
  });

  assert.deepEqual(errorCodeFailures(registry), [
    "codes[1].code: CF_CLI_USAGE duplicates codes[0]; retired identifiers cannot be reused",
  ]);
});

test("naming, ownership, and lifecycle failures are actionable", () => {
  const registry = {
    version: 1,
    codes: [
      {
        code: "bad-code",
        owner: "",
        lifecycle: "removed",
        introduced_in: "first",
        message: "Do not put user-facing copy here",
      },
    ],
  };

  assert.deepEqual(errorCodeFailures(registry), [
    "codes[0].code: bad-code must match CF_<OWNER>_<CONDITION>",
    "codes[0].owner: must be a lowercase module owner",
    "codes[0].lifecycle: removed must be active, deprecated, or retired",
    "codes[0].introduced_in: first must be a semantic product version",
    "codes[0].message: user-facing copy must remain outside the stable error-code registry",
  ]);
});

test("retirement metadata is required only for retired codes", () => {
  const registry = structuredClone(validRegistry);
  registry.codes[0].lifecycle = "retired";

  assert.deepEqual(errorCodeFailures(registry), [
    "codes[0].retired_in: retired code CF_CLI_USAGE requires a semantic product version",
  ]);
});

test("the identifier owner must match the registered owner", () => {
  const registry = structuredClone(validRegistry);
  registry.codes[0].owner = "profile";

  assert.deepEqual(errorCodeFailures(registry), [
    "codes[0].code: CF_CLI_USAGE must use owner prefix CF_PROFILE_",
  ]);
});

test("Go constants cannot drift from the central registry", () => {
  assert.deepEqual(
    goConstantFailures('const CLIUsage = "CF_CLI_OTHER"\n', validRegistry),
    [
      "codes.go: CF_CLI_OTHER is not in codes.json",
      "codes.json: CF_CLI_USAGE has no Go constant",
    ],
  );
});

test("historically retired identifiers remain retired and reserved", () => {
  const history = [
    {
      version: 1,
      codes: [
        {
          code: "CF_CLI_OLD",
          owner: "cli",
          lifecycle: "retired",
          introduced_in: "0.0.1-alpha",
          retired_in: "1.0.0",
        },
      ],
    },
  ];
  const reused = structuredClone(validRegistry);
  reused.codes.push({
    code: "CF_CLI_OLD",
    owner: "cli",
    lifecycle: "active",
    introduced_in: "2.0.0",
  });

  assert.deepEqual(historicalRetiredCodeFailures(reused, history), [
    "codes.json: historically retired identifier CF_CLI_OLD cannot become active",
  ]);
  assert.deepEqual(historicalRetiredCodeFailures(validRegistry, history), [
    "codes.json: retired identifier CF_CLI_OLD must remain registered",
  ]);
});

test("lifecycle versions use strict Semantic Version syntax", () => {
  for (const invalidVersion of ["01.2.3", "1.0.0-01", "1.0.0-.alpha"]) {
    const registry = structuredClone(validRegistry);
    registry.codes[0].introduced_in = invalidVersion;

    assert.deepEqual(errorCodeFailures(registry), [
      `codes[0].introduced_in: ${invalidVersion} must be a semantic product version`,
    ]);
  }
});
