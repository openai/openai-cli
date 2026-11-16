import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from reference_shadow import classify


class ShadowTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repo"
        self.repo.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Shadow test")
        self.git("config", "user.email", "shadow@example.invalid")
        self.write("api_reference/openapi.transformed.yml", b"openapi: 3.0.0\n")
        self.write(".castiron.stats.yml", b"generation_id: old\n")
        self.write("api_reference/go.mod", b"module example.invalid/reference\n")
        self.write("pkg/main.go", b"package main\n")
        self.base = self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], stderr=subprocess.DEVNULL).decode().strip()

    def write(self, name, content):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-m", "fixture")
        return self.git("rev-parse", "HEAD")

    def result(self):
        return classify(self.repo, self.base, self.commit())

    def test_reference_and_metadata_modifications(self):
        self.write("api_reference/openapi.transformed.yml", b"openapi: 3.1.0\n")
        self.write(".castiron.stats.yml", b"generation_id: new\n")
        result = self.result()
        self.assertEqual(result["classification"], "reference_only")
        self.assertEqual(result["changed_files"], 2)
        self.assertTrue(result["full_ci_required"])
        self.assertTrue(result["shadow_only"])

    def test_metadata_alone_is_distinct(self):
        self.write(".castiron.stats.yml", b"generation_id: new\n")
        self.assertEqual(self.result()["classification"], "metadata_only")

    def test_empty_diff_is_not_reference_only(self):
        self.assertEqual(classify(self.repo, self.base, self.base)["classification"], "no_change")

    def test_other_paths_require_full_ci(self):
        for name in ["pkg/main.go", "api_reference/go.mod", ".github/workflows/ci.yml", "new\n`name`.txt"]:
            with self.subTest(name=name):
                self.git("reset", "--hard", self.base)
                self.write(name, b"changed\n")
                result = self.result()
                self.assertEqual(result["classification"], "full_ci")
                self.assertNotIn(name, json.dumps(result))

    def test_mode_change(self):
        self.git("update-index", "--chmod=+x", "api_reference/openapi.transformed.yml")
        self.git("commit", "-m", "mode")
        self.assertEqual(classify(self.repo, self.base, self.git("rev-parse", "HEAD"))["classification"], "full_ci")

    def test_symlink_replacement(self):
        path = self.repo / "api_reference/openapi.transformed.yml"
        path.unlink()
        path.symlink_to("go.mod")
        self.assertEqual(self.result()["classification"], "full_ci")

    def test_deletion_and_rename(self):
        (self.repo / "api_reference/openapi.transformed.yml").unlink()
        self.assertEqual(self.result()["classification"], "full_ci")
        self.git("reset", "--hard", self.base)
        self.git("mv", "api_reference/openapi.transformed.yml", "api_reference/renamed.yml")
        self.assertEqual(self.result()["classification"], "full_ci")

    def test_new_allowlisted_file_does_not_qualify(self):
        (self.repo / ".castiron.stats.yml").unlink()
        self.base = self.commit()
        self.write(".castiron.stats.yml", b"new\n")
        self.assertEqual(self.result()["classification"], "full_ci")

    def test_binary_reference(self):
        self.write("api_reference/openapi.transformed.yml", b"\0binary\n")
        self.assertEqual(self.result()["classification"], "full_ci")

    def test_bad_or_missing_history(self):
        for base in ["--help", "", "a" * 40]:
            with self.subTest(base=base):
                self.assertEqual(classify(self.repo, base, self.base)["classification"], "unknown")

    def test_prepared_merge_and_depth_two_history(self):
        self.git("checkout", "-b", "feature")
        self.write("api_reference/openapi.transformed.yml", b"openapi: 3.1.0\n")
        pr_head = self.commit()
        self.git("checkout", "main")
        self.git("merge", "--no-ff", "feature", "-m", "merge")
        head = self.git("rev-parse", "HEAD")
        shallow = Path(self.temp.name) / "shallow"
        subprocess.check_call(["git", "clone", "--quiet", "--depth=2", self.repo.as_uri(), str(shallow)])
        self.assertEqual(classify(shallow, self.base, head, pr_head)["classification"], "reference_only")
        self.assertEqual(classify(shallow, pr_head, head, self.base)["reason"], "prepared_merge_mismatch")
        # An event for a different checkout must not describe the prepared tree.
        self.assertEqual(classify(shallow, self.base, pr_head, head)["classification"], "unknown")
        missing_parents = Path(self.temp.name) / "missing-parents"
        subprocess.check_call(["git", "clone", "--quiet", "--depth=1", self.repo.as_uri(), str(missing_parents)])
        self.assertEqual(classify(missing_parents, self.base, head, pr_head)["classification"], "unknown")


if __name__ == "__main__":
    unittest.main()
