const path = require("node:path");

function attestationEndpoint(registry, advertisedUrl) {
  return registry.replace(/\/+$/, "") + new URL(advertisedUrl).pathname;
}

async function fetchAttestations(packages) {
  if (!process.env.npm_execpath) {
    throw new Error("Attestation retrieval must run through the configured npm executable");
  }

  const npmRoot = path.dirname(path.dirname(process.env.npm_execpath));
  const npmDependency = (name) => require(require.resolve(name, { paths: [npmRoot] }));
  const Config = npmDependency("@npmcli/config");
  const { definitions, flatten, shorthands } = npmDependency("@npmcli/config/lib/definitions");
  const config = new Config({ npmPath: npmRoot, definitions, flatten, shorthands, argv: process.argv.slice(0, 2) });
  await config.load();

  const pacote = npmDependency("pacote");
  const fetch = npmDependency("npm-registry-fetch");
  const packageArg = npmDependency("npm-package-arg");
  const options = config.flat;

  return Promise.all(packages.map(async ({ name, version }) => {
    const spec = packageArg(`${name}@${version}`);
    const manifest = await pacote.manifest(spec, { ...options, fullMetadata: true, preferOnline: true });
    const attestations = manifest.dist?.attestations;
    if (!attestations?.url) {
      throw new Error(`Missing advertised provenance for ${name}@${version}`);
    }

    const registry = fetch.pickRegistry(spec, options);
    const response = await fetch(attestationEndpoint(registry, attestations.url), { ...options, registry, spec });
    const { attestations: attestationBundles } = await response.json();
    return { name, version, attestations, attestationBundles };
  }));
}

if (require.main === module) {
  fetchAttestations(JSON.parse(process.argv[2]))
    .then((verified) => process.stdout.write(JSON.stringify({ invalid: [], missing: [], verified })))
    .catch((error) => {
      console.error(`Unable to retrieve configured npm registry attestations: ${error.message}`);
      process.exitCode = 1;
    });
}

module.exports = { attestationEndpoint, fetchAttestations };
