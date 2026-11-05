"""Build and verify this workflow's fixed Linux AMD64 WSL payload bundles."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import time


PACKAGES = {
    "output.test": "./internal/output",
    "process.test": "./cmd/openai",
    "app.test": "./internal/app",
    "images.test": "./internal/commands/images",
    "imageprefs.test": "./internal/imageprefs",
    "clihelp.test": "./internal/clihelp",
    "autocomplete.test": "./internal/autocomplete",
    "openai": "./cmd/openai",
}
BUNDLES = {
    "wsl-pager": {"wsl-pager.test": "output.test", "wsl-process.test": "process.test"},
    "wsl-command-contracts": {"wsl-pager.test": "app.test", "wsl-process.test": "process.test",
                              "wsl-images.test": "images.test"},
    "wsl-local-state": {"wsl-pager.test": "images.test", "wsl-process.test": "process.test",
                        "wsl-imageprefs.test": "imageprefs.test", "wsl-clihelp.test": "clihelp.test",
                        "wsl-autocomplete.test": "autocomplete.test"},
    "wsl-shell-contracts": {"wsl-process.test": "process.test", "wsl-openai": "openai",
                            "wsl-autocomplete.test": "autocomplete.test"},
}
BUILD_ENV = {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "GOMAXPROCS": "2",
             "GOFLAGS": "-p=2 -mod=readonly", "GOTOOLCHAIN": "local"}


def digest(path, *, executable=False):
    if path.is_symlink() or not stat.S_ISREG(path.stat().st_mode):
        raise RuntimeError("Expected a regular WSL payload file")
    with path.open("rb") as stream:
        header = stream.read(20)
        if executable and (header[:6] != b"\x7fELF\x02\x01" or header[18:20] != b"\x3e\x00"):
            raise RuntimeError("Expected a Linux AMD64 ELF payload")
        result = hashlib.sha256(header)
        while chunk := stream.read(1024 * 1024):
            result.update(chunk)
    return result.hexdigest()


def context():
    source = Path(os.environ["RUNNER_TEMP"]) / "ci-inputs"
    commit, tree = subprocess.check_output(
        ["git", "rev-parse", "HEAD", "HEAD^{tree}"], text=True).splitlines()
    version = Path(".go-version").read_text().strip()
    if not all(re.fullmatch(r"[0-9a-f]{40}", value) for value in (commit, tree)):
        raise RuntimeError("Invalid prepared source identity")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
        raise RuntimeError("Expected a pinned Go version")
    run = int(os.environ["GITHUB_RUN_ID"])
    attempt = int(os.environ.get("WSL_PAYLOAD_ATTEMPT", os.environ["GITHUB_RUN_ATTEMPT"]))
    source_id = int(os.environ["WSL_SOURCE_ARTIFACT_ID"])
    if min(run, attempt, source_id) < 1 or attempt > int(os.environ["GITHUB_RUN_ATTEMPT"]):
        raise RuntimeError("Invalid WSL producer identity")
    sdk = source / "ci-sdk.zip"
    return {"schema": 1, "run_id": run, "producer_attempt": attempt,
            "source_artifact_id": source_id, "commit": commit, "tree": tree,
            "source_sha256": digest(source / "ci-source.tar.gz"),
            "sdk_sha256": digest(sdk) if sdk.exists() else None,
            "go_version": "go" + version, "host": {"os": "windows", "arch": "amd64"},
            "build_env": BUILD_ENV}


def build(destination, identity):
    environment = dict(os.environ, **BUILD_ENV)
    actual = json.loads(subprocess.check_output(
        ["go", "env", "-json", "GOVERSION", "GOHOSTOS", "GOHOSTARCH"], env=environment, text=True))
    if actual != {"GOVERSION": identity["go_version"], "GOHOSTOS": "windows", "GOHOSTARCH": "amd64"}:
        raise RuntimeError("Unexpected WSL producer toolchain or host")
    destination.mkdir(exist_ok=False)
    compiled = destination / "compiled"
    compiled.mkdir()
    records = {}
    for name, package in PACKAGES.items():
        started = time.monotonic()
        command = ["go", "build"] if name == "openai" else ["go", "test", "-c"]
        target = compiled / name
        subprocess.run(command + ["-o", str(target), package], env=environment, check=True)
        records[name] = {"package": package, "sha256": digest(target, executable=True),
                         "bytes": target.stat().st_size,
                         "build_seconds": round(time.monotonic() - started, 3)}
        print(json.dumps({"payload": name, **records[name]}), flush=True)
    subprocess.run(["go", "mod", "verify"], env=environment, check=True)
    for bundle, payloads in BUNDLES.items():
        directory = destination / bundle
        directory.mkdir()
        for name, compiled_name in payloads.items():
            shutil.copyfile(compiled / compiled_name, directory / name)
        manifest = {"context": identity, "bundle": bundle,
                    "payloads": {name: records[compiled_name] for name, compiled_name in payloads.items()}}
        (directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        verify(directory, bundle, identity)


def unique_object(pairs):
    result = {}
    for name, value in pairs:
        if name in result:
            raise RuntimeError("Duplicate WSL manifest key")
        result[name] = value
    return result


def verify(directory, bundle, identity):
    payloads = BUNDLES[bundle]
    if directory.is_symlink() or {path.name for path in directory.iterdir()} != set(payloads) | {"manifest.json"}:
        raise RuntimeError("Unexpected WSL artifact inventory")
    manifest_path = directory / "manifest.json"
    if manifest_path.stat().st_size > 16384:
        raise RuntimeError("Oversized WSL manifest")
    digest(manifest_path)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
    if (set(manifest) != {"context", "bundle", "payloads"} or manifest["context"] != identity
            or manifest["bundle"] != bundle or set(manifest["payloads"]) != set(payloads)):
        raise RuntimeError("WSL artifact does not match its consumer source or profile")
    for name, compiled_name in payloads.items():
        record = manifest["payloads"][name]
        target = directory / name
        if (set(record) != {"package", "sha256", "bytes", "build_seconds"}
                or record["package"] != PACKAGES[compiled_name]
                or record["bytes"] != target.stat().st_size
                or record["sha256"] != digest(target, executable=True)):
            raise RuntimeError("WSL payload package, digest, or size mismatch")
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["build", "verify"])
    parser.add_argument("directory", type=Path)
    parser.add_argument("--bundle", choices=BUNDLES)
    args = parser.parse_args()
    identity = context()
    if args.mode == "build":
        build(args.directory, identity)
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"attempt={identity['producer_attempt']}\n")
    else:
        if args.bundle is None:
            parser.error("verify requires --bundle")
        print(json.dumps(verify(args.directory, args.bundle, identity), indent=2))


if __name__ == "__main__":
    main()
