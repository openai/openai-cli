"""Run the reviewed source-preparation validator against real shallow Git data."""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/prepare-ci-source.yml"
CHECKOUT_REF = "${{ inputs.current-pr-merge && (github.event_name == 'pull_request' && github.ref || github.sha) || '' }}"


def validator_command():
    workflow = WORKFLOW.read_text()
    step = workflow.split("      - name: Verify selected pull request merge\n", 1)[1].split("\n      - name:", 1)[0]
    return textwrap.dedent(step.split("        run: |\n", 1)[1])


class SourceSelectionTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.origin = self.root / "origin.git"
        self.candidate = self.root / "candidate"
        self.env = dict(os.environ, GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.test",
                        GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.test",
                        GIT_AUTHOR_DATE="2026-01-01T00:00:00Z", GIT_COMMITTER_DATE="2026-01-01T00:00:00Z",
                        GITHUB_EVENT_PATH=str(self.root / "event.json"), GITHUB_REF="refs/pull/3/merge",
                        GITHUB_REPOSITORY="test/cli", RUNNER_TEMP=str(self.root))
        self.git(self.root, "init", "--bare", str(self.origin))
        self.tree = self.git(self.origin, "mktree", data="")
        self.base = self.commit("base")
        self.head = self.commit("head", self.base)
        self.old = self.commit("old merge", self.base, self.head)
        self.current = self.commit("regenerated merge", self.base, self.head)
        self.env["GITHUB_SHA"] = self.old
        self.git(self.origin, "update-ref", "refs/pull/3/merge", self.current)
        self.git(self.root, "init", str(self.candidate))
        self.git(self.candidate, "remote", "add", "origin", str(self.origin))
        self.event = {"number": 3, "repository": {"id": 1, "full_name": "test/cli"},
                      "pull_request": {"number": 3, "state": "open", "base": {
                          "sha": self.base, "repo": {"id": 1, "full_name": "test/cli"}},
                          "head": {"sha": self.head}}}

    def git(self, cwd, *args, data=None):
        return subprocess.check_output(["git", "-C", str(cwd), *args], input=data,
                                       text=True, stderr=subprocess.PIPE, env=self.env).strip()

    def commit(self, message, *parents):
        args = ["commit-tree", self.tree]
        for parent in parents:
            args.extend(["-p", parent])
        return self.git(self.origin, *args, data=message + "\n")

    def checkout(self, ref):
        self.git(self.candidate, "fetch", "--depth=1", "origin", ref)
        self.git(self.candidate, "checkout", "--detach", "FETCH_HEAD")

    def validate(self, event=None, **env):
        (self.root / "event.json").write_text(json.dumps(event if event is not None else self.event))
        return subprocess.run(["bash", "-e", "-c", validator_command()], cwd=self.root,
                              env=dict(self.env, **env), capture_output=True, text=True)

    def test_current_merge_replaces_stale_event_sha_and_is_packaged_exactly(self):
        self.checkout("refs/pull/3/merge")
        self.assertNotEqual(self.current, self.old)
        self.assertEqual(self.git(self.candidate, "rev-parse", "HEAD"), self.current)
        # Ordinary %P output hides shallow parents; the validator must read the
        # actual commit object rather than treating this as a parentless commit.
        self.assertEqual(self.git(self.candidate, "rev-parse", "--is-shallow-repository"), "true")
        result = self.validate()
        self.assertEqual(result.returncode, 0, result.stderr)
        command = WORKFLOW.read_text().split("      - name: Package candidate checkout\n", 1)[1].splitlines()[0]
        subprocess.run(["bash", "-e", "-c", command.removeprefix("        run: ")],
                       cwd=self.root, env=self.env, check=True, capture_output=True)
        with tarfile.open(self.root / "ci-source.tar.gz") as archive:
            self.assertEqual(archive.extractfile("./.git/HEAD").read().decode().strip(), self.current)
        # The existing recorder must bind actual Git HEAD, not stale GITHUB_SHA.
        for language in ["actions", "go", "javascript-typescript", "python"]:
            env = dict(self.env, CODEQL_LANGUAGE=language, SARIF_DIRECTORY=str(self.root),
                       GITHUB_RUN_ID="42", GITHUB_RUN_ATTEMPT="1")
            subprocess.run([sys.executable, "-I", str(ROOT / ".github/scripts/codeql_upload.py"), "record"],
                           cwd=self.candidate, env=env, check=True, capture_output=True)
            self.assertEqual(json.loads((self.root / "context.json").read_text()), {
                "sha": self.current, "ref": "refs/pull/3/merge", "language": language,
                "run_id": 42, "run_attempt": 1})

    def test_changed_or_unexpected_merge_parents_fail_before_packaging(self):
        new_base, new_head = self.commit("new base"), self.commit("new head", self.base)
        for parents in [(new_base, self.head), (self.base, new_head), (self.head, self.base),
                        (self.head,), (self.base, self.head, new_head)]:
            with self.subTest(parents=parents):
                changed = self.commit("changed merge", *parents)
                self.git(self.origin, "update-ref", "refs/pull/3/merge", changed)
                self.checkout("refs/pull/3/merge")
                result = self.validate()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("head or base changed", result.stderr)
                self.assertFalse((self.root / "ci-source.tar.gz").exists())

    def test_wrong_pr_repository_state_or_ref_is_rejected(self):
        self.checkout("refs/pull/3/merge")
        mutations = [lambda e: e.update(number=True),
                     lambda e: e["pull_request"].update(number=4),
                     lambda e: e["pull_request"].update(state="closed"),
                     lambda e: e["repository"].update(full_name="other/cli"),
                     lambda e: e["pull_request"]["base"]["repo"].update(id=2),
                     lambda e: e["pull_request"]["base"]["repo"].update(full_name="other/cli"),
                     lambda e: e["pull_request"]["head"].update(sha="--not-a-sha")]
        for mutation in mutations:
            event = copy.deepcopy(self.event)
            mutation(event)
            with self.subTest(event=event):
                self.assertNotEqual(self.validate(event).returncode, 0)
        for ref in ["refs/pull/4/merge", "refs/pull/3/head", "refs/heads/main"]:
            with self.subTest(ref=ref):
                self.assertNotEqual(self.validate(GITHUB_REF=ref).returncode, 0)

    def test_candidate_code_is_data_not_executed(self):
        self.checkout("refs/pull/3/merge")
        payload = "from pathlib import Path\nPath('executed').write_text('bad')\n"
        (self.root / "sitecustomize.py").write_text(payload)
        (self.candidate / "sitecustomize.py").write_text(payload)
        scripts = self.candidate / ".github/scripts"
        scripts.mkdir(parents=True)
        (scripts / "codeql_upload.py").write_text(payload)
        result = self.validate(PYTHONPATH=str(self.candidate))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / "executed").exists())
        self.assertFalse((self.candidate / "executed").exists())

    def test_non_pr_events_remain_pinned_when_branch_advances(self):
        # The workflow explicitly selects github.sha for opted-in non-PRs.
        # Check that exact-SHA checkout stays fixed while the branch advances.
        for ref in ["refs/heads/main", "refs/heads/gh-readonly-queue/main/pr-3-test"]:
            with self.subTest(ref=ref):
                self.git(self.origin, "update-ref", ref, self.current)
                self.checkout(self.old)
                self.assertEqual(self.git(self.candidate, "rev-parse", "HEAD"), self.old)


class WorkflowBoundaryTest(unittest.TestCase):
    def test_only_codeql_opts_in_and_other_events_use_exact_event_sha(self):
        source = WORKFLOW.read_text()
        self.assertIn("type: boolean\n        default: false", source)
        self.assertIn("ref: " + CHECKOUT_REF, source)
        self.assertIn("if: inputs.current-pr-merge && github.event_name == 'pull_request'", source)
        self.assertIn("persist-credentials: false", source)
        self.assertIn("python3 -I - <<'PYTHON'", validator_command())
        self.assertIn("current-pr-merge: true", (ROOT / ".github/workflows/codeql.yml").read_text())
        for name in ["ci.yml", "help-compatibility.yml"]:
            self.assertNotIn("current-pr-merge", (ROOT / ".github/workflows" / name).read_text())


if __name__ == "__main__":
    unittest.main()
