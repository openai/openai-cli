const fs = require("node:fs");
const path = require("node:path");

function resolveNativeBinary(toolsDirectory, platform = process.platform, architecture = process.arch) {
  const trustedDirectory = fs.realpathSync(toolsDirectory);
  const lockfile = JSON.parse(fs.readFileSync(path.join(trustedDirectory, "package-lock.json"), "utf8"));
  const packageName = `@stdy/cli-${platform}-${architecture}`;
  const lockfilePath = `node_modules/${packageName}`;
  const lockedPackage = lockfile.packages[lockfilePath];
  const wrapper = lockfile.packages["node_modules/@stdy/cli"];

  if (!lockedPackage || wrapper.optionalDependencies[packageName] !== lockedPackage.version) {
    throw new Error(`No locked Steady native package exists for ${platform}-${architecture}`);
  }

  if (!lockedPackage.os.includes(platform) || !lockedPackage.cpu.includes(architecture)) {
    throw new Error(`Locked Steady package ${packageName} does not match the current platform`);
  }

  const installedLockPath = path.join(trustedDirectory, "node_modules", ".package-lock.json");
  if (fs.realpathSync(installedLockPath) !== installedLockPath) {
    throw new Error("Steady installation lock resolves outside its locked tooling directory");
  }
  const installedLock = JSON.parse(fs.readFileSync(installedLockPath, "utf8"));

  for (const [installedPath, committedPackage] of [
    ["node_modules/@stdy/cli", wrapper],
    [lockfilePath, lockedPackage],
  ]) {
    if (!/^sha512-[A-Za-z0-9+/]{86}==$/.test(committedPackage.integrity)) {
      throw new Error(`Locked Steady package ${installedPath} has invalid SHA-512 integrity`);
    }

    const installed = installedLock.packages?.[installedPath];
    if (
      !installed ||
      installed.version !== committedPackage.version ||
      installed.resolved !== committedPackage.resolved ||
      installed.integrity !== committedPackage.integrity
    ) {
      throw new Error(`Installed Steady package ${installedPath} does not match locked integrity, version, or URL`);
    }
  }

  const packageDirectory = path.resolve(trustedDirectory, lockfilePath);
  const actualDirectory = fs.realpathSync(packageDirectory);
  if (actualDirectory !== packageDirectory) {
    throw new Error(`Steady native package resolves outside its locked installation: ${actualDirectory}`);
  }

  const installedPackage = JSON.parse(fs.readFileSync(path.join(packageDirectory, "package.json"), "utf8"));
  if (installedPackage.name !== packageName || installedPackage.version !== lockedPackage.version) {
    throw new Error(`Installed Steady native package does not match locked ${packageName}@${lockedPackage.version}`);
  }

  const executableName = platform === "win32" ? "steady.exe" : "steady";
  const executable = path.join(packageDirectory, "bin", executableName);
  const actualExecutable = fs.realpathSync(executable);
  if (actualExecutable !== executable) {
    throw new Error(`Steady native executable resolves outside its locked package: ${actualExecutable}`);
  }

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
