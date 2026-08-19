const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const { resolveNativeBinary } = require("./resolve-native");

const lockfile = JSON.parse(fs.readFileSync(path.join(__dirname, "package-lock.json"), "utf8"));
const wrapper = lockfile.packages["node_modules/@stdy/cli"];
const packages = ["@stdy/cli", ...Object.keys(wrapper.optionalDependencies)].map((name) => ({
  name,
  ...lockfile.packages[`node_modules/${name}`],
}));

function test(name, run) {
  try {
    run();
    console.log(`PASS ${name}`);
  } catch (error) {
    console.error(`FAIL ${name}: ${error.message}`);
    process.exitCode = 1;
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
    const committedLock = JSON.parse(fs.readFileSync(path.join(toolsDirectory, "package-lock.json"), "utf8"));
    const installedLock = {
      lockfileVersion: committedLock.lockfileVersion,
      packages: {
        "node_modules/@stdy/cli": committedLock.packages["node_modules/@stdy/cli"],
        [`node_modules/${packageName}`]: committedLock.packages[`node_modules/${packageName}`],
      },
    };
    fs.writeFileSync(path.join(toolsDirectory, "node_modules", ".package-lock.json"), JSON.stringify(installedLock));
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
  for (const pkg of packages) {
    assert.strictEqual(pkg.version, "0.22.2");
    assert.strictEqual(pkg.license, "MIT");
    assert.match(pkg.integrity, /^sha512-[A-Za-z0-9+/]{86}==$/);
    assert.strictEqual(new URL(pkg.resolved).hostname, "registry.npmjs.org");
  }
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

test("rejects an installed native package whose integrity differs from the committed lock", () => {
  withNativeFixture(({ toolsDirectory, packageName }) => {
    const installedLockPath = path.join(toolsDirectory, "node_modules", ".package-lock.json");
    const installedLock = JSON.parse(fs.readFileSync(installedLockPath, "utf8"));
    installedLock.packages[`node_modules/${packageName}`].integrity = `sha512-${Buffer.alloc(64).toString("base64")}`;
    fs.writeFileSync(installedLockPath, JSON.stringify(installedLock));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /does not match locked integrity/);
  });
});

test("rejects an installed wrapper whose integrity differs from the committed lock", () => {
  withNativeFixture(({ toolsDirectory }) => {
    const installedLockPath = path.join(toolsDirectory, "node_modules", ".package-lock.json");
    const installedLock = JSON.parse(fs.readFileSync(installedLockPath, "utf8"));
    installedLock.packages["node_modules/@stdy/cli"].integrity = `sha512-${Buffer.alloc(64).toString("base64")}`;
    fs.writeFileSync(installedLockPath, JSON.stringify(installedLock));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /does not match locked integrity/);
  });
});

test("rejects an installed native package from a different registry URL", () => {
  withNativeFixture(({ toolsDirectory, packageName }) => {
    const installedLockPath = path.join(toolsDirectory, "node_modules", ".package-lock.json");
    const installedLock = JSON.parse(fs.readFileSync(installedLockPath, "utf8"));
    installedLock.packages[`node_modules/${packageName}`].resolved = "https://untrusted.example/steady.tgz";
    fs.writeFileSync(installedLockPath, JSON.stringify(installedLock));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /does not match locked integrity, version, or URL/);
  });
});

test("rejects a committed native lock entry without valid SHA-512 integrity", () => {
  withNativeFixture(({ toolsDirectory, packageName }) => {
    const lockfilePath = path.join(toolsDirectory, "package-lock.json");
    const committedLock = JSON.parse(fs.readFileSync(lockfilePath, "utf8"));
    committedLock.packages[`node_modules/${packageName}`].integrity = "sha512-invalid";
    fs.writeFileSync(lockfilePath, JSON.stringify(committedLock));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /invalid SHA-512 integrity/);
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
