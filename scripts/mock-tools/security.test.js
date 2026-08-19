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

function withNativeFixture(run, layout = "hoisted") {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "openai-cli-steady-native-test-"));
  const repository = path.join(directory, "repository");
  const toolsDirectory = path.join(repository, "scripts", "mock-tools");
  const packageName = `@stdy/cli-${process.platform}-${process.arch}`;
  const packageVersion = packages.find(({ name }) => name === packageName).version;
  const wrapperDirectory = path.join(toolsDirectory, "node_modules", "@stdy", "cli");
  let packageDirectory = path.join(toolsDirectory, "node_modules", packageName);
  const executableName = process.platform === "win32" ? "steady.exe" : "steady";

  try {
    fs.mkdirSync(path.join(packageDirectory, "bin"), { recursive: true });
    fs.mkdirSync(wrapperDirectory, { recursive: true });
    fs.copyFileSync(path.join(__dirname, "package-lock.json"), path.join(toolsDirectory, "package-lock.json"));
    fs.writeFileSync(path.join(wrapperDirectory, "package.json"), JSON.stringify({ name: "@stdy/cli", version: wrapper.version }));
    fs.writeFileSync(path.join(packageDirectory, "package.json"), JSON.stringify({ name: packageName, version: packageVersion }));
    fs.writeFileSync(path.join(packageDirectory, "bin", executableName), "#!/bin/sh\nexit 0\n", { mode: 0o755 });

    if (layout === "nested" || layout === "shallow") {
      const nestedDirectory = path.join(wrapperDirectory, "node_modules", packageName);
      fs.mkdirSync(path.dirname(nestedDirectory), { recursive: true });
      fs.renameSync(packageDirectory, nestedDirectory);
      packageDirectory = nestedDirectory;
    } else if (layout === "linked") {
      const store = path.join(toolsDirectory, "node_modules", ".store");
      const storedWrapper = path.join(store, "wrapper", "node_modules", "@stdy", "cli");
      const storedNative = path.join(store, "native", "node_modules", packageName);
      fs.mkdirSync(path.dirname(storedWrapper), { recursive: true });
      fs.mkdirSync(path.dirname(storedNative), { recursive: true });
      fs.renameSync(wrapperDirectory, storedWrapper);
      fs.renameSync(packageDirectory, storedNative);
      fs.symlinkSync(storedWrapper, wrapperDirectory, "dir");
      const linkedNative = path.join(storedWrapper, "node_modules", packageName);
      fs.mkdirSync(path.dirname(linkedNative), { recursive: true });
      fs.symlinkSync(storedNative, linkedNative, "dir");
      packageDirectory = storedNative;
    }

    const executable = path.join(packageDirectory, "bin", executableName);
    run({ directory, repository, toolsDirectory, wrapperDirectory, packageDirectory, packageName, packageVersion, executable });
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
  }
});

for (const layout of ["hoisted", "nested", "shallow", "linked"]) {
  test(`resolves the exact locked native executable from a ${layout} installation`, () => {
    withNativeFixture(({ toolsDirectory, executable }) => {
      assert.strictEqual(resolveNativeBinary(toolsDirectory), fs.realpathSync(executable));
    }, layout);
  });
}

test("rejects a missing native package", () => {
  withNativeFixture(({ toolsDirectory, packageDirectory }) => {
    fs.rmSync(packageDirectory, { recursive: true, force: true });
    assert.throws(() => resolveNativeBinary(toolsDirectory), /Cannot find module/);
  });
});

test("rejects a missing native package even when an ancestor package exists", () => {
  withNativeFixture(({ repository, toolsDirectory, packageDirectory, packageName, packageVersion }) => {
    const ancestorPackage = path.join(repository, "node_modules", packageName);
    fs.mkdirSync(ancestorPackage, { recursive: true });
    fs.writeFileSync(path.join(ancestorPackage, "package.json"), JSON.stringify({ name: packageName, version: packageVersion }));
    fs.rmSync(packageDirectory, { recursive: true, force: true });
    assert.throws(() => resolveNativeBinary(toolsDirectory), /outside its locked tooling directory/);
  });
});

test("rejects an installed native package with a different version", () => {
  withNativeFixture(({ toolsDirectory, packageDirectory, packageName }) => {
    fs.writeFileSync(path.join(packageDirectory, "package.json"), JSON.stringify({ name: packageName, version: "0.22.1" }));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /does not match locked/);
  });
});

test("rejects an installed wrapper with a different version", () => {
  withNativeFixture(({ toolsDirectory, wrapperDirectory }) => {
    fs.writeFileSync(path.join(wrapperDirectory, "package.json"), JSON.stringify({ name: "@stdy/cli", version: "0.22.1" }));
    assert.throws(() => resolveNativeBinary(toolsDirectory), /does not match locked/);
  });
});

test("rejects a wrapper resolved from an ancestor installation", () => {
  withNativeFixture(({ repository, toolsDirectory, wrapperDirectory }) => {
    const ancestorWrapper = path.join(repository, "node_modules", "@stdy", "cli");
    fs.mkdirSync(ancestorWrapper, { recursive: true });
    fs.writeFileSync(path.join(ancestorWrapper, "package.json"), JSON.stringify({ name: "@stdy/cli", version: wrapper.version }));
    fs.rmSync(wrapperDirectory, { recursive: true, force: true });
    assert.throws(() => resolveNativeBinary(toolsDirectory), /outside its locked tooling directory/);
  });
});

test("rejects a native package linked outside the tooling directory", () => {
  withNativeFixture(({ repository, toolsDirectory, packageDirectory, packageName }) => {
    const externalPackage = path.join(repository, "scripts", "mock-tools-untrusted", packageName);
    fs.mkdirSync(path.dirname(externalPackage), { recursive: true });
    fs.renameSync(packageDirectory, externalPackage);
    fs.symlinkSync(externalPackage, packageDirectory, "dir");
    assert.throws(() => resolveNativeBinary(toolsDirectory), /outside its locked tooling directory/);
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
