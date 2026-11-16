"""Offline rejection controls for shared WSL executable artifacts."""

from copy import deepcopy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("wsl_payloads", Path(__file__).with_name("wsl_payloads.py"))
payloads = importlib.util.module_from_spec(spec)
spec.loader.exec_module(payloads)
ELF = b"\x7fELF\x02\x01" + bytes(12) + b"\x3e\x00"


class PayloadTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        inputs = self.root / "ci-inputs"
        inputs.mkdir()
        (inputs / "ci-source.tar.gz").write_bytes(b"synthetic source archive")
        self.env = {"RUNNER_TEMP": str(self.root), "GITHUB_RUN_ID": "101", "GITHUB_RUN_ATTEMPT": "2",
                    "WSL_SOURCE_ARTIFACT_ID": "202", "WSL_PAYLOAD_ATTEMPT": "1"}
        self.identity = self.context()
        self.destination = self.root / "built"

    def context(self):
        with patch.dict(os.environ, self.env), patch.object(payloads.subprocess, "check_output",
                return_value="a" * 40 + "\n" + "b" * 40 + "\n"):
            return payloads.context()

    def build(self):
        commands = []

        def run(command, **kwargs):
            commands.append(command)
            self.assertEqual({key: kwargs["env"][key] for key in payloads.BUILD_ENV}, payloads.BUILD_ENV)
            if "-o" in command:
                Path(command[command.index("-o") + 1]).write_bytes(ELF + command[-1].encode())

        toolchain = {"GOVERSION": self.identity["go_version"], "GOHOSTOS": "windows", "GOHOSTARCH": "amd64"}
        with patch.object(payloads.subprocess, "check_output", return_value=json.dumps(toolchain)), \
                patch.object(payloads.subprocess, "run", side_effect=run), patch("builtins.print"):
            payloads.build(self.destination, self.identity)
        return commands

    def test_seven_test_builds_one_cli_and_consumer_payloads(self):
        commands = self.build()
        tests = [command for command in commands if command[:3] == ["go", "test", "-c"]]
        self.assertEqual(len(tests), 7)
        self.assertEqual({command[-1] for command in tests}, {
            "./internal/output", "./cmd/openai", "./internal/app", "./internal/commands/images",
            "./internal/imageprefs", "./internal/clihelp", "./internal/autocomplete"})
        self.assertEqual(len([command for command in commands if command[:2] == ["go", "build"]]), 1)
        self.assertEqual(commands[-1], ["go", "mod", "verify"])
        self.assertEqual({name: len(paths) for name, paths in payloads.BUNDLES.items()}, {
            "wsl-pager": 2, "wsl-command-contracts": 3, "wsl-local-state": 5, "wsl-shell-contracts": 3})
        command = payloads.verify(self.destination / "wsl-command-contracts", "wsl-command-contracts", self.identity)
        self.assertEqual(command["payloads"]["wsl-pager.test"]["package"], "./internal/app")
        local = payloads.verify(self.destination / "wsl-local-state", "wsl-local-state", self.identity)
        self.assertEqual(local["payloads"]["wsl-pager.test"]["package"], "./internal/commands/images")

    def test_rejects_identity_inventory_package_hash_and_platform_changes(self):
        self.build()
        directory = self.destination / "wsl-pager"
        path = directory / "manifest.json"
        original = path.read_bytes()
        manifest = json.loads(original)
        for field in self.identity:
            with self.subTest(context=field):
                changed = deepcopy(manifest)
                changed["context"][field] = "wrong"
                path.write_text(json.dumps(changed), encoding="utf-8")
                with self.assertRaises(RuntimeError):
                    payloads.verify(directory, "wsl-pager", self.identity)
        for field, value in (("package", "./internal/app"), ("sha256", "0" * 64), ("bytes", 0)):
            with self.subTest(payload=field):
                changed = deepcopy(manifest)
                changed["payloads"]["wsl-pager.test"][field] = value
                path.write_text(json.dumps(changed), encoding="utf-8")
                with self.assertRaises(RuntimeError):
                    payloads.verify(directory, "wsl-pager", self.identity)
        path.write_bytes(original)
        with self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-command-contracts", self.identity)
        extra = directory / "unexpected"
        extra.write_bytes(b"extra")
        with self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-pager", self.identity)
        extra.unlink()
        target = directory / "wsl-pager.test"
        original_binary = target.read_bytes()
        for data in (original_binary[:-1], b"MZ" + original_binary[2:], original_binary[:18] + b"\xb7\x00" + original_binary[20:]):
            target.write_bytes(data)
            with self.assertRaises(RuntimeError):
                payloads.verify(directory, "wsl-pager", self.identity)
        target.unlink()
        with self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-pager", self.identity)
        target.write_bytes(original_binary)
        with patch.object(Path, "is_symlink", return_value=True), self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-pager", self.identity)
        with patch.object(Path, "is_symlink", lambda path: path.name == "wsl-pager.test"), self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-pager", self.identity)
        changed = deepcopy(manifest)
        changed["payloads"]["../wsl-pager.test"] = changed["payloads"].pop("wsl-pager.test")
        path.write_text(json.dumps(changed), encoding="utf-8")
        with self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-pager", self.identity)
        path.write_bytes(b'{"context": {}, "context": {}}')
        with self.assertRaises(RuntimeError):
            payloads.verify(directory, "wsl-pager", self.identity)

    def test_source_archive_sdk_and_attempt_are_bound(self):
        self.assertEqual(self.identity["producer_attempt"], 1)  # A failed consumer can reuse its successful producer.
        inputs = self.root / "ci-inputs"
        (inputs / "ci-source.tar.gz").write_bytes(b"another source archive")
        self.assertNotEqual(self.context()["source_sha256"], self.identity["source_sha256"])
        (inputs / "ci-sdk.zip").write_bytes(b"synthetic SDK snapshot")
        self.assertIsNotNone(self.context()["sdk_sha256"])
        self.env["WSL_PAYLOAD_ATTEMPT"] = "3"
        with self.assertRaises(RuntimeError):
            self.context()


if __name__ == "__main__":
    unittest.main()
