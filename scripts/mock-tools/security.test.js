const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const { resolveNativeBinary } = require("./resolve-native");
const { createAuditWorkspace, lockedPackages, verifyAuditReport } = require("./verify-provenance");

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

function provenanceStatement(pkg) {
  return {
    subject: [{ digest: { sha512: Buffer.from(pkg.integrity.slice("sha512-".length), "base64").toString("hex") } }],
    predicate: {
      buildDefinition: {
        externalParameters: {
          workflow: {
            repository: "https://github.com/dgellow/steady",
            ref: `refs/tags/v${pkg.version}`,
          },
        },
      },
    },
  };
}

function auditReport() {
  return {
    invalid: [],
    missing: [],
    verified: packages.map((pkg) => ({
      name: pkg.name,
      version: pkg.version,
      attestations: { provenance: { predicateType: "https://slsa.dev/provenance/v1" } },
      attestationBundles: [{
        predicateType: "https://slsa.dev/provenance/v1",
        bundle: {
          dsseEnvelope: {
            payload: Buffer.from(JSON.stringify(provenanceStatement(pkg))).toString("base64"),
          },
        },
      }],
    })),
  };
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

test("accepts verified provenance for all six locked artifacts", () => {
  assert.doesNotThrow(() => verifyAuditReport(auditReport(), packages));
});

test("rejects a skipped native platform", () => {
  const report = auditReport();
  report.verified.pop();
  assert.throws(() => verifyAuditReport(report, packages), /all 6 locked Steady packages/);
});

test("rejects stripped SLSA provenance metadata", () => {
  const report = auditReport();
  delete report.verified[1].attestations.provenance;
  assert.throws(() => verifyAuditReport(report, packages), /Missing SLSA provenance attestation/);
});

test("rejects a missing cryptographically verified provenance bundle", () => {
  const report = auditReport();
  report.verified[1].attestationBundles = [];
  assert.throws(() => verifyAuditReport(report, packages), /Missing cryptographically verified SLSA bundle/);
});

test("rejects an attested digest that does not match the lockfile", () => {
  const report = auditReport();
  const statement = provenanceStatement(packages[1]);
  statement.subject[0].digest.sha512 = "0".repeat(128);
  report.verified[1].attestationBundles[0].bundle.dsseEnvelope.payload =
    Buffer.from(JSON.stringify(statement)).toString("base64");
  assert.throws(() => verifyAuditReport(report, packages), /does not match locked SHA-512 integrity/);
});

test("rejects provenance from another repository or release", () => {
  const report = auditReport();
  const statement = provenanceStatement(packages[1]);
  statement.predicate.buildDefinition.externalParameters.workflow.repository = "https://github.com/untrusted/steady";
  report.verified[1].attestationBundles[0].bundle.dsseEnvelope.payload =
    Buffer.from(JSON.stringify(statement)).toString("base64");
  assert.throws(() => verifyAuditReport(report, packages), /does not match the expected Steady release/);
});

test("rejects missing or invalid registry signatures", () => {
  const missing = auditReport();
  missing.missing.push({ name: packages[0].name });
  assert.throws(() => verifyAuditReport(missing, packages), /missing package signatures/);

  const invalid = auditReport();
  invalid.invalid.push({ name: packages[0].name });
  assert.throws(() => verifyAuditReport(invalid, packages), /invalid package signatures or attestations/);
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
