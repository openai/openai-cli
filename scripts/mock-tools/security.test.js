const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const { resolveNativeBinary } = require("./resolve-native");
const { createAuditWorkspace, lockedPackages, verifyAuditOutput, verifyProvenance } = require("./verify-provenance");

const packages = lockedPackages(__dirname);

function test(name, run) {
  try {
    run();
    console.log(`PASS ${name}`);
  } catch (error) {
    console.error(`FAIL ${name}: ${error.message}`);
    process.exitCode = 1;
  }
}

function withAuditStub(output, exitCode, run) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "openai-cli-steady-audit-test-"));
  const invocationLog = path.join(directory, "invocations.log");
  const previousPath = process.env.PATH;
  const previousConsoleLog = console.log;

  fs.writeFileSync(path.join(directory, "npm"), [
    "#!/usr/bin/env node",
    "const fs = require('node:fs');",
    `fs.appendFileSync(${JSON.stringify(invocationLog)}, process.argv.slice(2, 4).join(' ') + '\\n');`,
    "if (process.argv[2] !== 'audit') { process.stderr.write('unexpected second npm invocation'); process.exit(23); }",
    `process.stdout.write(${JSON.stringify(output)});`,
    `process.exit(${exitCode});`,
  ].join("\n"), { mode: 0o755 });

  process.env.PATH = `${directory}${path.delimiter}${previousPath}`;
  console.log = () => {};

  try {
    run(invocationLog);
  } finally {
    process.env.PATH = previousPath;
    console.log = previousConsoleLog;
    fs.rmSync(directory, { recursive: true, force: true });
  }
}

function withNativeFixture(run) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "openai-cli-steady-native-test-"));
  const repository = path.join(directory, "repository");
  const toolsDirectory = path.join(repository, "scripts", "mock-tools");
  const packageName = `@stdy/cli-${process.platform}-${process.arch}`;
  const packageVersion = packages.find(({ name }) => name === packageName).version;
  const packageDirectory = path.join(toolsDirectory, "node_modules", packageName);
  const executable = path.join(packageDirectory, "bin", process.platform === "win32" ? "steady.exe" : "steady");

  try {
    fs.mkdirSync(path.dirname(executable), { recursive: true });
    fs.copyFileSync(path.join(__dirname, "package-lock.json"), path.join(toolsDirectory, "package-lock.json"));
    fs.writeFileSync(path.join(packageDirectory, "package.json"), JSON.stringify({ name: packageName, version: packageVersion }));
    fs.writeFileSync(executable, "#!/bin/sh\nexit 0\n", { mode: 0o755 });
    run({ directory, repository, toolsDirectory, packageDirectory, packageName, packageVersion, executable });
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}

test("locks the wrapper and every supported native platform", () => {
  assert.strictEqual(packages.length, 6);
  assert.deepStrictEqual(
    packages.map(({ name }) => name).sort(),
    [
      "@stdy/cli",
      "@stdy/cli-darwin-arm64",
      "@stdy/cli-darwin-x64",
      "@stdy/cli-linux-arm64",
      "@stdy/cli-linux-x64",
      "@stdy/cli-win32-x64",
    ],
  );
});

test("audits all native platforms without downloading their executables", () => {
  const workspace = createAuditWorkspace(packages);
  try {
    const manifest = JSON.parse(fs.readFileSync(path.join(workspace, "package.json"), "utf8"));
    assert.deepStrictEqual(Object.keys(manifest.dependencies).sort(), packages.map(({ name }) => name).sort());
    for (const pkg of packages) {
      const directory = path.join(workspace, "node_modules", pkg.name);
      assert.deepStrictEqual(fs.readdirSync(directory), ["package.json"]);
    }
  } finally {
    fs.rmSync(workspace, { recursive: true, force: true });
  }
});

test("accepts complete npm 10 and npm 11 signature and attestation summaries", () => {
  const output = "audited 6 packages in 1s\n\n6 packages have verified registry signatures\n\n6 packages have verified attestations\n";
  assert.doesNotThrow(() => verifyAuditOutput(output, packages.length));
});

test("uses exactly one canonical npm audit without an independent attestation fetch", () => {
  const output = "6 packages have verified registry signatures\n6 packages have verified attestations\n";
  withAuditStub(output, 0, (invocationLog) => {
    assert.doesNotThrow(() => verifyProvenance(__dirname));
    assert.deepStrictEqual(fs.readFileSync(invocationLog, "utf8").trim().split("\n"), ["audit signatures"]);
  });
});

test("rejects a failed canonical npm audit even when its coverage summary is complete", () => {
  const output = "6 packages have verified registry signatures\n6 packages have verified attestations\n";
  withAuditStub(output, 9, () => {
    assert.throws(() => verifyProvenance(__dirname), /npm signature and provenance verification failed/);
  });
});

test("rejects an incomplete registry-signature summary", () => {
  const output = "5 packages have verified registry signatures\n6 packages have verified attestations\n";
  assert.throws(() => verifyAuditOutput(output, packages.length), /registry signatures for all 6/);
});

test("rejects a missing or incomplete provenance summary", () => {
  const signatures = "6 packages have verified registry signatures\n";
  assert.throws(() => verifyAuditOutput(signatures, packages.length), /provenance for all 6/);
  assert.throws(
    () => verifyAuditOutput(`${signatures}5 packages have verified attestations\n`, packages.length),
    /provenance for all 6/,
  );
});

test("executes only the checked, exact-version native executable", () => {
  withNativeFixture(({ toolsDirectory, executable }) => {
    assert.strictEqual(resolveNativeBinary(toolsDirectory), fs.realpathSync(executable));
  });
});

test("rejects a missing native package even when an ancestor package exists", () => {
  withNativeFixture(({ repository, toolsDirectory, packageDirectory, packageName, packageVersion }) => {
    const ancestorPackage = path.join(repository, "node_modules", packageName);
    fs.mkdirSync(ancestorPackage, { recursive: true });
    fs.writeFileSync(path.join(ancestorPackage, "package.json"), JSON.stringify({ name: packageName, version: packageVersion }));
    fs.rmSync(packageDirectory, { recursive: true, force: true });
    assert.throws(() => resolveNativeBinary(toolsDirectory), /ENOENT/);
  });
});

test("rejects an installed native package with a different version", () => {
  withNativeFixture(({ toolsDirectory, packageDirectory, packageName }) => {
    fs.writeFileSync(path.join(packageDirectory, "package.json"), JSON.stringify({ name: packageName, version: "0.22.1" }));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /does not match locked/);
  });
});

test("rejects a native executable that escapes its locked package", () => {
  withNativeFixture(({ directory, toolsDirectory, executable }) => {
    const external = path.join(directory, "untrusted-steady");
    fs.writeFileSync(external, "#!/bin/sh\nexit 0\n", { mode: 0o755 });
    fs.rmSync(executable);
    fs.symlinkSync(external, executable);
    assert.throws(() => resolveNativeBinary(toolsDirectory), /outside its locked package/);
  });
});
