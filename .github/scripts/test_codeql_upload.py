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

START1, START2 = "2026-01-01T00:00:00Z", "2026-01-01T00:10:00Z"
HEAD, BASE, MERGE = "a" * 40, "b" * 40, "c" * 40
REPO = {"id": 1, "full_name": "test/cli", "owner": {"login": "test"}}


class PublisherTest(unittest.TestCase):
    def setUp(self):
        self.run = dict(id=42, run_attempt=2, repository=REPO, head_repository=REPO,
                        path=publisher.WORKFLOW, status="completed", conclusion="success", run_started_at=START2,
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
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"] = {"jobs": [
            dict(name=f"CodeQL analysis ({language})", run_id=42, run_attempt=2,
                 head_sha=HEAD, status="completed", conclusion="success", started_at=START2) for language in publisher.CATEGORIES]}
        self.responses["actions/runs/42/attempts/2"] = dict(self.run)
        self.target = {"sha": MERGE, "ref": "refs/pull/3/merge"}
        self.writes = []

    def get(self, route, **kwargs):
        self.assertTrue(route.startswith("repos/test/cli/"))
        route = route.removeprefix("repos/test/cli/")
        if route.startswith("actions/workflows/codeql.yml/runs?"):
            return {"workflow_runs": [copy.deepcopy(self.run)]}
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
        self.assertEqual(len(self.writes), len(publisher.CATEGORIES))
        for payload in self.writes:
            self.assertEqual(payload["commit_sha"], MERGE)
            self.assertEqual(payload["ref"], self.target["ref"])
        self.writes.clear()
        self.responses["actions/artifacts/1/zip"] = self.archive(extra=True)
        with self.assertRaises(RuntimeError):
            publisher.publish(self.get, "test/cli", 1, {"id": 42, "run_attempt": 2})
        self.assertEqual(self.writes, [])

    def test_required_statuses_wait_for_successful_publication(self):
        for index, language in enumerate(publisher.CATEGORIES):
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language)
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
            for i, lang in enumerate(publisher.CATEGORIES)]}
        publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p.get("state", "sarif") for p in self.writes],
                         ["pending"] * len(publisher.CATEGORIES) + ["sarif"] * len(publisher.CATEGORIES) + ["success"] * len(publisher.CATEGORIES))
        self.writes.clear()
        self.responses["actions/artifacts/1/zip"] = self.archive(extra=True)
        with self.assertRaises(RuntimeError):
            publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES) + ["failure"] * len(publisher.CATEGORIES))

    def test_regenerated_merge_still_requires_exact_analyzed_sha(self):
        for index, language in enumerate(publisher.CATEGORIES):
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(
                language, mutate=lambda context, report: context.update(sha="d" * 40))
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
            for i, lang in enumerate(publisher.CATEGORIES)]}
        # Even if a regenerated merge has the same parents/tree, old reports
        # cannot be attributed to its different commit object.
        with self.assertRaises(publisher.StaleRun):
            publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES))

    def test_merge_ref_moving_during_artifact_download_cannot_publish(self):
        for index, language in enumerate(publisher.CATEGORIES):
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language)
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
            for i, lang in enumerate(publisher.CATEGORIES)]}

        def get(route, **kwargs):
            result = self.get(route, **kwargs)
            if route.endswith("actions/artifacts/1/zip"):
                replacement = "d" * 40
                self.pr["merge_commit_sha"] = replacement
                self.responses["git/ref/pull/3/merge"]["object"]["sha"] = replacement
                self.responses["git/commits/" + replacement] = {"parents": [{"sha": BASE}, {"sha": HEAD}]}
            return result

        with self.assertRaises(publisher.StaleRun):
            publisher.report(get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES))

    def test_pending_and_failed_analysis_cannot_pass_required_status(self):
        for status, conclusion in [("in_progress", None), ("completed", "failure"), ("completed", "cancelled")]:
            with self.subTest(status=status, conclusion=conclusion):
                self.run.update(status=status, conclusion=conclusion)
                self.writes.clear()
                if status == "completed":
                    with self.assertRaises(RuntimeError):
                        publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
                    self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES) + ["failure"] * len(publisher.CATEGORIES))
                else:
                    publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
                    self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES))

    def test_processing_error_and_timeout_never_pass(self):
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
            for i, lang in enumerate(publisher.CATEGORIES)]}
        for i, lang in enumerate(publisher.CATEGORIES):
            self.responses[f"actions/artifacts/{i}/zip"] = self.archive(lang)
        for outcome in ["failed", "pending"]:
            self.writes.clear()
            def get(route, **kwargs):
                if route.endswith("code-scanning/sarifs/safe-id"):
                    return {"processing_status": outcome}
                return self.get(route, **kwargs)
            with self.assertRaises(RuntimeError):
                publisher.report(get, "test/cli", 1, dict(id=42, run_attempt=2), sleep=lambda _: None)
            self.assertNotIn("success", [p.get("state") for p in self.writes])
            self.assertEqual([p["state"] for p in self.writes[-2:]], ["failure", "failure"])

    def test_superseded_run_cannot_overwrite_statuses(self):
        def get(route, **kwargs):
            if "actions/workflows/codeql.yml/runs?" in route:
                return {"workflow_runs": [dict(self.run, id=43)]}
            return self.get(route, **kwargs)
        with self.assertRaises(publisher.StaleRun):
            publisher.report(get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual(self.writes, [])

    def partial_rerun(self):
        actions = dict(name="CodeQL analysis (actions)", run_id=42, run_attempt=1,
                       head_sha=HEAD, status="completed", conclusion="success", started_at=START1)
        go = dict(actions, name="CodeQL analysis (go)", run_attempt=2, started_at=START2)
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"] = {"jobs": [go]}
        self.responses["actions/runs/42/attempts/1"] = dict(self.run, run_attempt=1, run_started_at=START1, conclusion="failure")
        self.responses["actions/runs/42/attempts/1/jobs?per_page=100&page=1"] = {"jobs": [
            actions, dict(go, run_attempt=1, conclusion="failure")]}
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=0, name="codeql-sarif-42-1-actions", expired=False),
            dict(id=1, name="codeql-sarif-42-2-go", expired=False)]}
        self.responses["actions/artifacts/0/zip"] = self.archive("actions", mutate=lambda c, r: c.update(run_attempt=1))
        self.responses["actions/artifacts/1/zip"] = self.archive("go")
        # Other languages completed in attempt 1 and were not rerun.
        for index, language in enumerate(list(publisher.CATEGORIES)[2:], start=2):
            self.responses["actions/runs/42/attempts/1/jobs?per_page=100&page=1"]["jobs"].append(
                dict(actions, name=f"CodeQL analysis ({language})"))
            self.responses["actions/runs/42/artifacts?per_page=100&page=1"]["artifacts"].append(
                dict(id=index, name=f"codeql-sarif-42-1-{language}", expired=False))
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(
                language, mutate=lambda c, r: c.update(run_attempt=1))
        return actions, go

    def test_partial_rerun_reuses_only_successful_language_execution(self):
        actions, go = self.partial_rerun()
        publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p.get("state", "sarif") for p in self.writes],
                         ["pending"] * len(publisher.CATEGORIES) + ["sarif"] * len(publisher.CATEGORIES) + ["success"] * len(publisher.CATEGORIES))
        # Some job listings retain successful jobs with their original attempt.
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"]["jobs"].append(actions)
        self.writes.clear()
        publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p["state"] for p in self.writes[-2:]], ["success", "success"])

    def test_partial_rerun_copied_jobs_are_not_new_executions(self):
        actions, _ = self.partial_rerun()
        # Live GitHub responses copy successful jobs into attempt 2 with new
        # IDs and run_attempt=2, but retain the attempt-1 execution timestamps.
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"]["jobs"].append(
            dict(actions, id=999, run_attempt=2))
        publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p["state"] for p in self.writes[-2:]], ["success", "success"])

    def test_partial_rerun_rejects_failed_or_different_source_job(self):
        for mutation in [lambda job: job.update(conclusion="failure"),
                         lambda job: job.update(head_sha=BASE),
                         lambda job: job.update(run_id=9)]:
            self.setUp()
            actions, _ = self.partial_rerun()
            mutation(actions)
            with self.assertRaises(RuntimeError):
                publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
            self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES) + ["failure"] * len(publisher.CATEGORIES))

    def test_new_successful_job_cannot_fall_back_to_older_artifact(self):
        actions, _ = self.partial_rerun()
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"]["jobs"].append(
            dict(actions, run_attempt=2, started_at=START2))
        with self.assertRaises(RuntimeError):
            publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertNotIn("success", [p.get("state") for p in self.writes])
        self.assertFalse(any("sarif" in p for p in self.writes))


if __name__ == "__main__":
    unittest.main()
