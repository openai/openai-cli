const fs = require("node:fs");
const path = require("node:path");

function requireInside(directory, candidate, description, boundary = "its locked tooling directory") {
  const relative = path.relative(directory, candidate);
  if (!relative || relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    throw new Error(`${description} resolves outside ${boundary}: ${candidate}`);
  }
}

function resolveLockedPackage(toolsDirectory, name, searchDirectory, expectedVersion) {
  const manifest = require.resolve(`${name}/package.json`, { paths: [searchDirectory] });
  const packageDirectory = fs.realpathSync(path.dirname(manifest));
  requireInside(toolsDirectory, packageDirectory, `Steady package ${name}`);

  const installed = JSON.parse(fs.readFileSync(path.join(packageDirectory, "package.json"), "utf8"));
  if (installed.name !== name || installed.version !== expectedVersion) {
    throw new Error(`Installed Steady package does not match locked ${name}@${expectedVersion}`);
  }

  return packageDirectory;
}

function resolveNativeBinary(toolsDirectory, platform = process.platform, architecture = process.arch) {
  const trustedDirectory = fs.realpathSync(toolsDirectory);
  const lockfile = JSON.parse(fs.readFileSync(path.join(trustedDirectory, "package-lock.json"), "utf8"));
  const packageName = `@stdy/cli-${platform}-${architecture}`;
  const lockedPackage = lockfile.packages[`node_modules/${packageName}`];
  const wrapper = lockfile.packages["node_modules/@stdy/cli"];

  if (!lockedPackage || wrapper?.optionalDependencies?.[packageName] !== lockedPackage.version) {
    throw new Error(`No locked Steady native package exists for ${platform}-${architecture}`);
  }

  if (!lockedPackage.os?.includes(platform) || !lockedPackage.cpu?.includes(architecture)) {
    throw new Error(`Locked Steady package ${packageName} does not match the current platform`);
  }

  const wrapperDirectory = resolveLockedPackage(trustedDirectory, "@stdy/cli", trustedDirectory, wrapper.version);
  const packageDirectory = resolveLockedPackage(trustedDirectory, packageName, wrapperDirectory, lockedPackage.version);

  const executableName = platform === "win32" ? "steady.exe" : "steady";
  const executable = path.join(packageDirectory, "bin", executableName);
  const actualExecutable = fs.realpathSync(executable);
  requireInside(packageDirectory, actualExecutable, "Steady native executable", "its locked package");

  fs.accessSync(actualExecutable, fs.constants.X_OK);
  return actualExecutable;
}

if (require.main === module) {
  try {
    process.stdout.write(resolveNativeBinary(__dirname));
  } catch (error) {
    console.error(`Unable to verify the locked Steady native executable: ${error.message}`);
    process.exitCode = 1;
  }
}

module.exports = { resolveNativeBinary };
