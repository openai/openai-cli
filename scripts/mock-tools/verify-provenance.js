const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

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

    // npm 10 and 11 omit attestation coverage from their JSON audit reports.
    // Their shared summary and successful exit are the compatibility boundary;
    // npm alone fetches and cryptographically verifies every attestation.
    verifyAuditOutput(audit.stdout, packages.length);
    for (const { name, version } of packages) {
      console.log(`npm verified registry signatures and attestations for ${name}@${version}`);
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

module.exports = { createAuditWorkspace, lockedPackages, verifyAuditOutput, verifyProvenance };
