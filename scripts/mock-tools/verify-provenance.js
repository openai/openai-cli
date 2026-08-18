const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const PROVENANCE_PREDICATE = "https://slsa.dev/provenance/v1";
const STEADY_REPOSITORY = "https://github.com/dgellow/steady";

function lockedPackages(toolsDirectory) {
  const lockfile = JSON.parse(fs.readFileSync(path.join(toolsDirectory, "package-lock.json"), "utf8"));
  const wrapper = lockfile.packages["node_modules/@stdy/cli"];
  const expectedVersions = {
    "@stdy/cli": wrapper.version,
    ...wrapper.optionalDependencies,
  };

  return Object.entries(expectedVersions).map(([name, version]) => {
    const entry = lockfile.packages[`node_modules/${name}`];
    if (
      !entry ||
      entry.version !== version ||
      typeof entry.integrity !== "string" ||
      !entry.integrity.startsWith("sha512-")
    ) {
      throw new Error(`Missing or inconsistent SHA-512 lock entry for ${name}@${version}`);
    }
    return { name, version, integrity: entry.integrity };
  });
}

function createAuditWorkspace(packages) {
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), "openai-cli-steady-audit-"));
  const dependencies = Object.fromEntries(packages.map(({ name, version }) => [name, version]));
  fs.writeFileSync(
    path.join(workspace, "package.json"),
    JSON.stringify({ name: "openai-cli-steady-provenance-audit", private: true, dependencies }),
  );

  // npm verifies registry signatures and attestations from package metadata;
  // manifests let one host audit every platform without downloading its binary.
  for (const { name, version } of packages) {
    const directory = path.join(workspace, "node_modules", name);
    fs.mkdirSync(directory, { recursive: true });
    fs.writeFileSync(path.join(directory, "package.json"), JSON.stringify({ name, version }));
  }

  return workspace;
}

function verifyAuditReport(report, packages) {
  if (!Array.isArray(report.invalid) || report.invalid.length > 0) {
    throw new Error("npm reported invalid package signatures or attestations");
  }
  if (!Array.isArray(report.missing) || report.missing.length > 0) {
    throw new Error("npm reported missing package signatures");
  }
  if (!Array.isArray(report.verified) || report.verified.length !== packages.length) {
    throw new Error(`Expected verified provenance for all ${packages.length} locked Steady packages`);
  }

  for (const expected of packages) {
    const verified = report.verified.find(({ name }) => name === expected.name);
    if (!verified || verified.version !== expected.version) {
      throw new Error(`Missing verified provenance for ${expected.name}@${expected.version}`);
    }
    if (verified.attestations?.provenance?.predicateType !== PROVENANCE_PREDICATE) {
      throw new Error(`Missing SLSA provenance attestation for ${expected.name}@${expected.version}`);
    }

    const provenance = verified.attestationBundles?.find(
      ({ predicateType }) => predicateType === PROVENANCE_PREDICATE,
    );
    if (!provenance?.bundle?.dsseEnvelope?.payload) {
      throw new Error(`Missing cryptographically verified SLSA bundle for ${expected.name}@${expected.version}`);
    }

    const statement = JSON.parse(Buffer.from(provenance.bundle.dsseEnvelope.payload, "base64").toString("utf8"));
    const expectedDigest = Buffer.from(expected.integrity.slice("sha512-".length), "base64").toString("hex");
    if (statement.subject?.[0]?.digest?.sha512 !== expectedDigest) {
      throw new Error(`Verified provenance does not match locked SHA-512 integrity for ${expected.name}`);
    }

    const workflow = statement.predicate?.buildDefinition?.externalParameters?.workflow;
    if (workflow?.repository !== STEADY_REPOSITORY || workflow.ref !== `refs/tags/v${expected.version}`) {
      throw new Error(`Verified provenance does not match the expected Steady release for ${expected.name}`);
    }
  }
}

function verifyAuditOutput(output, count) {
  const packageLabel = count === 1 ? "package has a" : "packages have";
  const signatures = `${count} ${packageLabel} verified registry signature${count === 1 ? "" : "s"}`;
  const attestations = `${count} ${packageLabel} verified attestation${count === 1 ? "" : "s"}`;
  const lines = output.split(/\r?\n/);

  if (!lines.includes(signatures)) {
    throw new Error(`Expected verified registry signatures for all ${count} locked Steady packages`);
  }
  if (!lines.includes(attestations)) {
    throw new Error(`Expected verified provenance for all ${count} locked Steady packages`);
  }
}

function fetchAttestationReport(packages) {
  const configuration = spawnSync("npm", ["config", "get", "registry"], { encoding: "utf8" });
  if (configuration.error) throw configuration.error;
  if (configuration.status !== 0) {
    throw new Error(`Unable to determine the npm registry: ${configuration.stderr}`);
  }

  const configuredRegistry = configuration.stdout.trim();
  const registry = configuredRegistry.endsWith("/") ? configuredRegistry : `${configuredRegistry}/`;
  const verified = packages.map(({ name, version }) => {
    const endpoint = new URL(`-/npm/v1/attestations/${encodeURIComponent(name)}@${version}`, registry);
    const response = spawnSync("curl", ["--fail", "--silent", "--show-error", endpoint.href], {
      encoding: "utf8",
      maxBuffer: 16 * 1024 * 1024,
    });

    if (response.error) throw response.error;
    if (response.status !== 0) {
      throw new Error(`Unable to inspect signed provenance for ${name}@${version}: ${response.stderr}`);
    }

    const attestationBundles = JSON.parse(response.stdout).attestations;
    const provenance = attestationBundles?.find(({ predicateType }) => predicateType === PROVENANCE_PREDICATE);
    return {
      name,
      version,
      attestations: provenance ? { provenance: { predicateType: provenance.predicateType } } : {},
      attestationBundles,
    };
  });

  return { invalid: [], missing: [], verified };
}

function verifyProvenance(toolsDirectory) {
  const packages = lockedPackages(toolsDirectory);
  const workspace = createAuditWorkspace(packages);

  try {
    const audit = spawnSync(
      "npm",
      [
        "audit",
        "signatures",
        "--prefix",
        workspace,
        "--include=dev",
        "--include=optional",
      ],
      { encoding: "utf8", maxBuffer: 16 * 1024 * 1024 },
    );

    if (audit.error) throw audit.error;
    if (audit.status !== 0) {
      throw new Error(`npm signature and provenance verification failed:\n${audit.stderr}${audit.stdout}`);
    }

    // npm 10 verifies attestations but omits verified bundles from JSON reports.
    // Its stable human-readable summary proves complete cryptographic coverage;
    // read each verified bundle separately to check its locked digest and source.
    verifyAuditOutput(audit.stdout, packages.length);
    verifyAuditReport(fetchAttestationReport(packages), packages);
    for (const { name, version } of packages) {
      console.log(`Verified signed SLSA provenance for ${name}@${version}`);
    }
  } finally {
    fs.rmSync(workspace, { recursive: true, force: true });
  }
}

if (require.main === module) {
  try {
    verifyProvenance(__dirname);
  } catch (error) {
    console.error(`Steady provenance verification failed: ${error.message}`);
    process.exitCode = 1;
  }
}

module.exports = { createAuditWorkspace, lockedPackages, verifyAuditOutput, verifyAuditReport, verifyProvenance };
