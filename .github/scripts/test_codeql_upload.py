"""Offline tests of the privileged publisher's input boundary."""
import copy
import importlib.util
import io
import json
from pathlib import Path
import unittest
import zipfile

spec = importlib.util.spec_from_file_location("publisher", Path(__file__).with_name("codeql_upload.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)

HEAD, BASE, MERGE = "a" * 40, "b" * 40, "c" * 40
REPO = {"id": 1, "full_name": "test/cli", "owner": {"login": "test"}}


class PublisherTest(unittest.TestCase):
    def setUp(self):
        self.run = dict(id=42, run_attempt=2, repository=REPO, head_repository=REPO,
                        path=publisher.WORKFLOW, status="completed", conclusion="success",
                        event="pull_request", head_sha=HEAD, head_branch="feature",
                        pull_requests=[{"number": 3, "base": {"sha": BASE}}])
        self.pr = dict(number=3, state="open", merge_commit_sha=MERGE,
                       head=dict(sha=HEAD, ref="feature", repo=REPO),
                       base=dict(sha=BASE, ref="main", repo=REPO))
        self.responses = {
            "actions/runs/42": self.run,
            "pulls/3": self.pr,
            "git/ref/pull/3/merge": {"ref": "refs/pull/3/merge", "object": {"sha": MERGE}},
            "git/commits/" + MERGE: {"parents": [{"sha": BASE}, {"sha": HEAD}]},
        }
        self.target = {"sha": MERGE, "ref": "refs/pull/3/merge"}
        self.writes = []

    def get(self, route, **kwargs):
        self.assertTrue(route.startswith("repos/test/cli/"))
        route = route.removeprefix("repos/test/cli/")
        if "payload" in kwargs:
            self.writes.append(kwargs["payload"])
            return {"id": "safe-id"}
        if route == "code-scanning/sarifs/safe-id":
            return {"processing_status": "complete"}
        return copy.deepcopy(self.responses[route])

    def destination(self):
        return publisher.destination(self.get, "test/cli", 1, {"id": 42, "run_attempt": 2})[1]

    def archive(self, language="go", mutate=None, extra=False):
        context = dict(self.target, run_id=42, run_attempt=2, language=language)
        report = {"version": "2.1.0", "runs": [{"tool": {"driver": {"name": "CodeQL"}},
                  "automationDetails": {"id": "attacker-category/"},
                  "versionControlProvenance": [{"revisionId": BASE}], "results": []}]}
        if mutate:
            mutate(context, report)
        data = io.BytesIO()
        with zipfile.ZipFile(data, "w") as archive:
            archive.writestr("context.json", json.dumps(context))
            archive.writestr("upload.sarif", json.dumps(report))
            if extra:
                archive.writestr("../../payload.py", "raise Exception('executed')")
        return data.getvalue()

    def test_pr_merge_and_fork(self):
        self.assertEqual(self.destination(), self.target)
        fork = dict(id=9, owner={"login": "fork"})
        self.run["head_repository"] = self.pr["head"]["repo"] = fork
        self.run["pull_requests"] = []
        self.responses[f"commits/{HEAD}/pulls?per_page=100&page=1"] = [{"number": 3}]
        self.assertEqual(self.destination(), self.target)

    def test_rejects_changed_attempt_head_base_and_parents(self):
        for mutate in [lambda: self.run.update(run_attempt=3),
                       lambda: self.pr["head"].update(sha=BASE),
                       lambda: self.pr["base"].update(sha=HEAD),
                       lambda: self.responses["git/commits/" + MERGE].update(parents=[{"sha": HEAD}])]:
            with self.subTest(mutate=mutate):
                self.setUp()
                mutate()
                with self.assertRaises(publisher.StaleRun):
                    self.destination()

    def test_rejects_other_workflow_repository_event(self):
        for field, value in [("path", ".github/workflows/evil.yml"),
                             ("repository", dict(id=9, full_name="test/cli")),
                             ("event", "workflow_dispatch")]:
            with self.subTest(field=field):
                self.setUp()
                self.run[field] = value
                with self.assertRaises(RuntimeError):
                    self.destination()

    def test_main_and_merge_queue(self):
        for event, branch in [("push", "main"), ("merge_group", "gh-readonly-queue/main/pr-3-test")]:
            self.run.update(event=event, head_branch=branch)
            route = "git/ref/heads/" + publisher.quote(branch, safe="")
            self.responses[route] = {"ref": "refs/heads/" + branch, "object": {"sha": HEAD}}
            self.assertEqual(self.destination(), {"sha": HEAD, "ref": "refs/heads/" + branch})
            self.responses[route]["object"]["sha"] = BASE
            with self.assertRaises(publisher.StaleRun):
                self.destination()

    def test_artifact_cannot_choose_category_or_revision(self):
        report = publisher.read_artifact(self.archive(), self.run, self.target, "go")
        self.assertEqual(report["runs"][0]["automationDetails"]["id"], publisher.CATEGORIES["go"] + "/")
        self.assertNotIn("versionControlProvenance", report["runs"][0])
        for mutation in [lambda c, r: c.update(sha=HEAD), lambda c, r: c.update(ref="refs/heads/main"),
                         lambda c, r: c.update(run_attempt=1)]:
            with self.assertRaises(publisher.StaleRun):
                publisher.read_artifact(self.archive(mutate=mutation), self.run, self.target, "go")
        for mutation in [lambda c, r: r["runs"].append(r["runs"][0]),
                         lambda c, r: r["runs"][0]["tool"]["driver"].update(name="other-tool")]:
            with self.assertRaises(RuntimeError):
                publisher.read_artifact(self.archive(mutate=mutation), self.run, self.target, "go")
        with self.assertRaises(RuntimeError):
            publisher.read_artifact(self.archive(extra=True), self.run, self.target, "go")

    def test_publish_and_no_writes_on_malformed_second_artifact(self):
        artifacts = []
        for index, language in enumerate(publisher.CATEGORIES):
            artifacts.append(dict(id=index, name=f"codeql-sarif-42-2-{language}", expired=False))
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language)
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": artifacts}
        publisher.publish(self.get, "test/cli", 1, {"id": 42, "run_attempt": 2})
        self.assertEqual(len(self.writes), 2)
        for payload in self.writes:
            self.assertEqual(payload["commit_sha"], MERGE)
            self.assertEqual(payload["ref"], self.target["ref"])
        self.writes.clear()
        self.responses["actions/artifacts/1/zip"] = self.archive(extra=True)
        with self.assertRaises(RuntimeError):
            publisher.publish(self.get, "test/cli", 1, {"id": 42, "run_attempt": 2})
        self.assertEqual(self.writes, [])


if __name__ == "__main__":
    unittest.main()
