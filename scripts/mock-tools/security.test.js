const assert = require("node:assert");
const { spawnSync } = require("node:child_process");
const crypto = require("node:crypto");
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
    fs.copyFileSync(path.join(__dirname, "package.json"), path.join(toolsDirectory, "package.json"));
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

function runMock(repository, toolsDirectory, configuration = {}) {
  fs.copyFileSync(path.join(__dirname, "..", "mock"), path.join(repository, "scripts", "mock"));
  fs.copyFileSync(path.join(__dirname, "resolve-native.js"), path.join(toolsDirectory, "resolve-native.js"));

  return spawnSync(path.join(repository, "scripts", "mock"), ["fixture.openapi.yml"], {
    cwd: repository,
    encoding: "utf8",
    env: {
      ...process.env,
      npm_config_cache: path.join(repository, "empty-cache"),
      npm_config_offline: "true",
      OPENAI_API_KEY: "synthetic-sdk95-canary",
      STEADY_CANARY_FILE: path.join(repository, "stolen-canary"),
      ...configuration,
    },
  });
}

function installCanaryExecutable(executable) {
  fs.writeFileSync(executable, '#!/bin/sh\nprintf "%s" "$OPENAI_API_KEY" > "$STEADY_CANARY_FILE"\n', { mode: 0o755 });
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

test("rejects a missing native package despite an in-directory NODE_PATH fallback", () => {
  withNativeFixture(({ toolsDirectory, packageDirectory, packageName }) => {
    const fallback = path.join(toolsDirectory, "unreviewed");
    const forgedPackage = path.join(fallback, packageName);
    fs.mkdirSync(path.dirname(forgedPackage), { recursive: true });
    fs.renameSync(packageDirectory, forgedPackage);

    const script = 'const { resolveNativeBinary } = require(process.argv[1]); process.stdout.write(resolveNativeBinary(process.argv[2]));';
    const result = spawnSync(process.execPath, ["-e", script, path.join(__dirname, "resolve-native.js"), toolsDirectory], {
      encoding: "utf8",
      env: { ...process.env, NODE_PATH: fallback },
    });

    assert.notStrictEqual(result.status, 0, `accepted unreviewed NODE_PATH executable: ${result.stdout}`);
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

test("rejects a forged prior installation when ambient npm dry-run succeeds", () => {
  withNativeFixture(({ repository, toolsDirectory, executable }) => {
    installCanaryExecutable(executable);
    const result = runMock(repository, toolsDirectory, { npm_config_dry_run: "true" });

    assert.notStrictEqual(result.status, 0, "ambient dry-run launched a forged prior installation");
    assert.strictEqual(fs.existsSync(path.join(repository, "stolen-canary")), false);
  });
});

test("ignores a sibling shrinkwrap containing counterfeit same-version archives", () => {
  withNativeFixture(({ repository, toolsDirectory, wrapperDirectory, packageDirectory, packageName, executable }) => {
    installCanaryExecutable(executable);
    fs.writeFileSync(
      path.join(wrapperDirectory, "package.json"),
      JSON.stringify({ name: "@stdy/cli", version: wrapper.version, optionalDependencies: { [packageName]: wrapper.version } }),
    );

    const archiveDirectory = path.join(repository, "counterfeit-archives");
    fs.mkdirSync(archiveDirectory);
    const shrinkwrap = JSON.parse(fs.readFileSync(path.join(toolsDirectory, "package-lock.json"), "utf8"));

    for (const [name, directory] of [["@stdy/cli", wrapperDirectory], [packageName, packageDirectory]]) {
      const packed = spawnSync("npm", ["pack", "--ignore-scripts", "--silent", "--pack-destination", archiveDirectory, directory], {
        encoding: "utf8",
      });
      assert.strictEqual(packed.status, 0, packed.stderr);
      const archive = path.join(archiveDirectory, packed.stdout.trim().split("\n").pop());
      const entry = shrinkwrap.packages[`node_modules/${name}`];
      entry.resolved = `file:${archive}`;
      entry.integrity = `sha512-${crypto.createHash("sha512").update(fs.readFileSync(archive)).digest("base64")}`;
    }

    fs.writeFileSync(path.join(toolsDirectory, "npm-shrinkwrap.json"), JSON.stringify(shrinkwrap));
    const result = runMock(repository, toolsDirectory, { npm_config_dry_run: "false" });

    assert.notStrictEqual(result.status, 0, "counterfeit shrinkwrap archives launched their native executable");
    assert.strictEqual(fs.existsSync(path.join(repository, "stolen-canary")), false);
  });
});
