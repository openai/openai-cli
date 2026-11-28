"""Offline tests of the privileged publisher's input boundary."""
import base64
import copy
import gzip
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
            "git/ref/heads/main": {"ref": "refs/heads/main", "object": {"type": "commit", "sha": BASE}},
            "git/ref/heads/next": {"ref": "refs/heads/next", "object": {"type": "commit", "sha": BASE}},
            "git/ref/pull/3/merge": {"ref": "refs/pull/3/merge", "object": {"sha": MERGE}},
            "git/commits/" + MERGE: {"parents": [{"sha": BASE}, {"sha": HEAD}]},
        }
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"] = {"jobs": [
            dict(name=f"CodeQL analysis ({language})", run_id=42, run_attempt=2,
                 head_sha=HEAD, status="completed", conclusion="success", started_at=START2) for language in publisher.CATEGORIES]}
        self.responses["actions/runs/42/attempts/2"] = dict(self.run)
        self.target = {"sha": MERGE, "ref": "refs/pull/3/merge"}
        self.writes = []
        self.write_routes = []

    def get(self, route, **kwargs):
        self.assertTrue(route.startswith("repos/test/cli/"))
        route = route.removeprefix("repos/test/cli/")
        if route.startswith("actions/workflows/") and "/runs?" in route:
            return {"workflow_runs": [copy.deepcopy(self.run)]}
        if "payload" in kwargs:
            self.writes.append(kwargs["payload"])
            self.write_routes.append(route)
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
                       lambda: self.responses["git/ref/heads/main"]["object"].update(sha=HEAD),
                       lambda: self.responses["git/commits/" + MERGE].update(parents=[{"sha": HEAD}])]:
            with self.subTest(mutate=mutate):
                self.setUp()
                mutate()
                with self.assertRaises(publisher.StaleRun):
                    self.destination()

    def test_stale_base_snapshots_use_live_target(self):
        self.pr["base"]["sha"] = "d" * 40
        self.run["pull_requests"][0]["base"]["sha"] = "e" * 40
        self.assertEqual(self.destination(), self.target)

    def test_invalid_live_target_is_rejected(self):
        for ref in [{"ref": "refs/heads/other", "object": {"type": "commit", "sha": BASE}},
                    {"ref": "refs/heads/main", "object": {"type": "tag", "sha": BASE}},
                    {"ref": "refs/heads/main", "object": {"type": "commit", "sha": "invalid"}}]:
            with self.subTest(ref=ref):
                self.responses["git/ref/heads/main"] = ref
                with self.assertRaises(RuntimeError):
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

    def test_release_targets_and_merge_queue(self):
        for event, branch in [("push", "main"), ("push", "next"),
                              ("merge_group", "gh-readonly-queue/main/pr-3-test"),
                              ("merge_group", "gh-readonly-queue/next/pr-3-test")]:
            self.run.update(event=event, head_branch=branch)
            route = "git/ref/heads/" + publisher.quote(branch, safe="")
            self.responses[route] = {"ref": "refs/heads/" + branch, "object": {"sha": HEAD}}
            self.assertEqual(self.destination(), {"sha": HEAD, "ref": "refs/heads/" + branch})
            self.responses[route]["object"]["sha"] = BASE
            with self.assertRaises(publisher.StaleRun):
                self.destination()

    def test_next_pr_and_fallback_discovery(self):
        self.pr["base"]["ref"] = "next"
        self.assertEqual(self.destination(), self.target)
        self.run["pull_requests"] = []
        self.responses[f"commits/{HEAD}/pulls?per_page=100&page=1"] = []
        self.responses["pulls?state=open&head=test%3Afeature&per_page=100&page=1"] = [{"number": 3}]
        self.assertEqual(self.destination(), self.target)

    def test_rejects_unapproved_targets_and_queue_prefixes(self):
        for branch in ["feature", "next-door", "gh-readonly-queue/next", "gh-readonly-queue/next-door/pr-3"]:
            for event in ["push", "merge_group"]:
                with self.subTest(branch=branch, event=event):
                    self.run.update(event=event, head_branch=branch)
                    with self.assertRaises(RuntimeError):
                        self.destination()
        self.setUp()
        self.pr["base"]["ref"] = "next-door"
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

    def test_publish_preserves_each_language_analysis(self):
        artifacts, expected_reports = [], []
        languages = ("actions", "go", "javascript-typescript", "python")
        for index, language in enumerate(languages):
            rules = [{"id": f"test/{language}/{i}", "shortDescription": {"text": "Synthetic rule"}}
                     for i in range(2)]
            findings = [{"ruleId": rule["id"], "ruleIndex": i, "level": "warning",
                         "message": {"text": f"Synthetic {language} finding {i}: café\nsecond line"},
                         "locations": [{"physicalLocation": {
                             "artifactLocation": {"uri": f"synthetic/{language}/sample.txt"},
                             "region": {"startLine": i + 1, "startColumn": 3}}}],
                         "partialFingerprints": {"synthetic/v1": f"{language}-{i}"}}
                        for i, rule in enumerate(rules)]
            expected = {"version": "2.1.0", "runs": [{
                "tool": {"driver": {"name": "CodeQL", "version": "0.0.0", "rules": rules},
                         "extensions": [{"name": f"synthetic/{language}/queries", "rules": [{
                             "id": f"test/{language}/extension-rule",
                             "shortDescription": {"text": "Synthetic extension rule"}}]}]},
                "automationDetails": {"id": publisher.CATEGORIES[language] + "/"},
                "invocations": [{"executionSuccessful": True}],
                "results": findings, "properties": {"syntheticLanguage": language}}]}
            expected_reports.append(expected)

            def populate(context, report):
                report.clear()
                report.update(copy.deepcopy(expected))
                run = report["runs"][0]
                run["automationDetails"] = {"id": "untrusted-category/"}
                run["versionControlProvenance"] = [{"revisionId": BASE}]
                run["runAggregates"] = [{"id": "untrusted-aggregate/"}]
                run["tool"]["driver"]["guid"] = "00000000-0000-4000-8000-000000000000"

            artifacts.append(dict(id=index, name=f"codeql-sarif-42-2-{language}", expired=False))
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language, mutate=populate)
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": artifacts}

        publisher.publish(self.get, "test/cli", 1, {"id": 42, "run_attempt": 2})

        self.assertEqual(len(self.writes), len(expected_reports))
        for language, payload, expected in zip(languages, self.writes, expected_reports):
            with self.subTest(language=language):
                self.assertEqual(payload["commit_sha"], MERGE)
                self.assertEqual(payload["ref"], self.target["ref"])
                self.assertEqual(payload["tool_name"], "CodeQL")
                decoded = json.loads(gzip.decompress(base64.b64decode(payload["sarif"], validate=True)))
                self.assertEqual(decoded, expected)

    def test_required_statuses_wait_for_successful_publication(self):
        for index, language in enumerate(publisher.CATEGORIES):
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language)
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
            for i, lang in enumerate(publisher.CATEGORIES)]}
        publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p.get("state", "sarif") for p in self.writes],
                         ["pending"] * len(publisher.CATEGORIES) + ["sarif"] * len(publisher.CATEGORIES) + ["success"] * len(publisher.CATEGORIES))
        self.assertEqual(self.write_routes,
                         [f"statuses/{MERGE}"] * len(publisher.CATEGORIES) +
                         ["code-scanning/sarifs"] * len(publisher.CATEGORIES) +
                         [f"statuses/{MERGE}"] * len(publisher.CATEGORIES))
        for payload in self.writes:
            if "sarif" in payload:
                self.assertEqual(payload["commit_sha"], MERGE)
                self.assertEqual(payload["ref"], "refs/pull/3/merge")
        self.writes.clear()
        self.write_routes.clear()
        self.responses["actions/artifacts/1/zip"] = self.archive(extra=True)
        with self.assertRaises(RuntimeError):
            publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
        self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES) + ["failure"] * len(publisher.CATEGORIES))
        self.assertEqual(self.write_routes, [f"statuses/{MERGE}"] * (2 * len(publisher.CATEGORIES)))

    def test_pending_statuses_use_source_head_for_push_and_merge_queue(self):
        for event, branch in [("push", "main"), ("push", "next"),
                              ("merge_group", "gh-readonly-queue/main/pr-3-test"),
                              ("merge_group", "gh-readonly-queue/next/pr-3-test")]:
            with self.subTest(event=event, branch=branch):
                self.setUp()
                self.run.update(event=event, head_branch=branch, status="in_progress", conclusion=None)
                self.responses["git/ref/heads/" + publisher.quote(branch, safe="")] = {
                    "ref": "refs/heads/" + branch, "object": {"sha": HEAD}}
                publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
                self.assertEqual(self.write_routes, [f"statuses/{HEAD}"] * len(publisher.CATEGORIES))
                self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES))

    def test_changed_pr_head_or_base_cannot_set_statuses(self):
        for field in ("head", "base"):
            with self.subTest(field=field):
                self.setUp()
                self.pr[field]["sha"] = "d" * 40
                with self.assertRaises(publisher.StaleRun):
                    publisher.report(self.get, "test/cli", 1, dict(id=42, run_attempt=2))
                self.assertEqual(self.write_routes, [])

    def test_pr_head_or_base_moving_during_download_cannot_pass(self):
        for field in ("head", "base"):
            with self.subTest(field=field):
                self.setUp()
                for index, language in enumerate(publisher.CATEGORIES):
                    self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language)
                self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
                    dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
                    for i, lang in enumerate(publisher.CATEGORIES)]}

                def get(route, **kwargs):
                    result = self.get(route, **kwargs)
                    if route.endswith("actions/artifacts/1/zip"):
                        self.pr[field]["sha"] = "d" * 40
                    return result

                with self.assertRaises(publisher.StaleRun):
                    publisher.report(get, "test/cli", 1, dict(id=42, run_attempt=2))
                self.assertEqual(self.write_routes, [f"statuses/{MERGE}"] * len(publisher.CATEGORIES))
                self.assertEqual([p["state"] for p in self.writes], ["pending"] * len(publisher.CATEGORIES))

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

    def test_live_target_moving_during_artifact_download_cannot_publish(self):
        for index, language in enumerate(publisher.CATEGORIES):
            self.responses[f"actions/artifacts/{index}/zip"] = self.archive(language)
        self.responses["actions/runs/42/artifacts?per_page=100&page=1"] = {"artifacts": [
            dict(id=i, name=f"codeql-sarif-42-2-{lang}", expired=False)
            for i, lang in enumerate(publisher.CATEGORIES)]}

        def get(route, **kwargs):
            result = self.get(route, **kwargs)
            if route.endswith("actions/artifacts/1/zip"):
                self.responses["git/ref/heads/main"]["object"]["sha"] = "d" * 40
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

    def test_parallel_main_next_pr_runs_do_not_supersede_each_other(self):
        for target, other_target in [("main", "next"), ("next", "main")]:
            with self.subTest(target=target):
                self.pr["base"]["ref"] = target
                other_pr = copy.deepcopy(self.pr)
                other_pr["number"] = 4
                other_pr["base"]["ref"] = other_target
                self.responses["pulls/4"] = other_pr
                other = dict(self.run, id=43, pull_requests=[{"number": 4}])
                def get(route, **kwargs):
                    if "actions/workflows/codeql.yml/runs?" in route:
                        return {"workflow_runs": [other]}
                    return self.get(route, **kwargs)
                self.assertEqual(publisher.destination(get, "test/cli", 1,
                                                       dict(id=42, run_attempt=2))[1], self.target)

    def test_supersession_fallback_requires_unique_same_pr(self):
        other = dict(self.run, id=43, pull_requests=[])
        self.responses[f"commits/{HEAD}/pulls?per_page=100&page=1"] = [{"number": 3}]
        def get(route, **kwargs):
            if "actions/workflows/codeql.yml/runs?" in route:
                return {"workflow_runs": [other]}
            return self.get(route, **kwargs)
        with self.assertRaises(publisher.StaleRun):
            publisher.destination(get, "test/cli", 1, dict(id=42, run_attempt=2))
        other_pr = copy.deepcopy(self.pr)
        other_pr["number"] = 4
        other_pr["base"]["ref"] = "next"
        self.responses["pulls/4"] = other_pr
        self.responses[f"commits/{HEAD}/pulls?per_page=100&page=1"].append({"number": 4})
        self.assertEqual(publisher.destination(get, "test/cli", 1,
                                               dict(id=42, run_attempt=2))[1], self.target)

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


class CIPublisherTest(unittest.TestCase):
    def setUp(self):
        self.fixture = PublisherTest()
        self.fixture.setUp()
        self.run = self.fixture.run
        self.responses = self.fixture.responses
        self.run["path"] = publisher.CI_WORKFLOW
        self.run["referenced_workflows"] = [{
            "path": f"test/cli/{publisher.SOURCE_WORKFLOW}@{MERGE}",
            "ref": "refs/pull/3/merge", "sha": MERGE}]
        self.responses["actions/runs/42/attempts/2"] = dict(self.run)
        self.responses["actions/runs/42/attempts/1"] = dict(self.run, run_attempt=1, run_started_at=START1)
        self.responses["actions/runs/42/attempts/1/jobs?per_page=100&page=1"] = {"jobs": []}
        self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"]["jobs"] = [
            dict(name=name, run_id=42, run_attempt=2, head_sha=HEAD,
                 status="completed", conclusion="success", started_at=START2)
            for name in ("source / prepare", "build", "lint", "test")]

    def report(self, get=None):
        publisher.report_ci(get or self.fixture.get, "test/cli", 1, dict(id=42, run_attempt=2))

    def test_ci_gates_use_verified_merge_without_candidate_artifacts(self):
        self.report()
        self.assertEqual(self.fixture.write_routes, [f"statuses/{MERGE}"] * 6)
        self.assertEqual([p["context"] for p in self.fixture.writes], ["build", "lint", "test"] * 2)
        self.assertEqual([p["state"] for p in self.fixture.writes], ["pending"] * 3 + ["success"] * 3)
        self.assertFalse(any("artifacts" in route for route in self.responses))

    def test_source_metadata_must_match_exact_repository_ref_and_sha(self):
        original = copy.deepcopy(self.run["referenced_workflows"][0])
        variants = [[], [original, original],
                    [dict(original, path=f"other/cli/{publisher.SOURCE_WORKFLOW}@{MERGE}")],
                    [dict(original, ref="refs/pull/4/merge")],
                    [dict(original, sha=HEAD)],
                    [dict(original, path=f"test/cli/{publisher.SOURCE_WORKFLOW}@{HEAD}")]]
        for references in variants:
            with self.subTest(references=references):
                self.run["referenced_workflows"] = references
                self.fixture.writes.clear()
                with self.assertRaises(RuntimeError):
                    self.report()
                self.assertEqual([p["state"] for p in self.fixture.writes],
                                 ["pending"] * 3 + ["failure"] * 3)

    def test_pending_and_failed_runs_do_not_pass(self):
        for status, conclusion in [("in_progress", None), ("completed", "failure"),
                                   ("completed", "cancelled")]:
            with self.subTest(status=status, conclusion=conclusion):
                self.setUp()
                self.run.update(status=status, conclusion=conclusion)
                if status == "completed":
                    self.responses["actions/runs/42/attempts/2/jobs?per_page=100&page=1"]["jobs"][-1]["conclusion"] = conclusion
                    with self.assertRaises(RuntimeError):
                        self.report()
                else:
                    self.report()
                self.assertNotIn("success", [p["state"] for p in self.fixture.writes])

    def test_unrelated_job_failure_does_not_change_required_gate_results(self):
        self.run["conclusion"] = "failure"
        self.report()
        self.assertEqual([p["state"] for p in self.fixture.writes], ["pending"] * 3 + ["success"] * 3)

    def test_missing_duplicate_skipped_or_failed_gate_cannot_pass(self):
        route = "actions/runs/42/attempts/2/jobs?per_page=100&page=1"
        for mutation in [lambda jobs: jobs.pop(), lambda jobs: jobs.append(dict(jobs[-1])),
                         lambda jobs: jobs[-1].update(conclusion="skipped"),
                         lambda jobs: jobs[-1].update(conclusion="failure")]:
            with self.subTest(mutation=mutation):
                self.setUp()
                mutation(self.responses[route]["jobs"])
                with self.assertRaises(RuntimeError):
                    self.report()
                self.assertNotIn("success", [p["state"] for p in self.fixture.writes])

    def test_partial_rerun_binds_carried_forward_jobs_and_source(self):
        jobs_route = "actions/runs/42/attempts/2/jobs?per_page=100&page=1"
        original = copy.deepcopy(self.responses[jobs_route]["jobs"])
        old = [dict(job, run_attempt=1, started_at=START1) for job in original]
        self.responses["actions/runs/42/attempts/1"] = dict(self.run, run_attempt=1, run_started_at=START1)
        self.responses["actions/runs/42/attempts/1/jobs?per_page=100&page=1"] = {"jobs": old}
        # GitHub carries successful jobs forward with new IDs/attempt numbers,
        # while preserving original execution times.
        self.responses[jobs_route]["jobs"] = [dict(job, started_at=START1) for job in original[:-1]] + [original[-1]]
        self.report()
        self.assertEqual([p["state"] for p in self.fixture.writes][-3:], ["success"] * 3)
        self.fixture.writes.clear()
        self.responses["actions/runs/42/attempts/1"]["referenced_workflows"] = [{
            "path": f"test/cli/{publisher.SOURCE_WORKFLOW}@{HEAD}",
            "ref": "refs/pull/3/merge", "sha": HEAD}]
        with self.assertRaises(RuntimeError):
            self.report()
        self.assertEqual([p["state"] for p in self.fixture.writes], ["pending"] * 3 + ["failure"] * 3)

    def test_base_head_or_merge_movement_cannot_publish_success(self):
        for field in ("head", "base", "merge_commit_sha"):
            with self.subTest(field=field):
                self.setUp()
                def get(route, **kwargs):
                    result = self.fixture.get(route, **kwargs)
                    if route.endswith("attempts/2/jobs?per_page=100&page=1"):
                        if field == "merge_commit_sha":
                            self.fixture.pr[field] = "d" * 40
                        else:
                            self.fixture.pr[field]["sha"] = "d" * 40
                    return result
                with self.assertRaises(publisher.StaleRun):
                    self.report(get)
                self.assertEqual([p["state"] for p in self.fixture.writes], ["pending"] * 3)

    def test_parallel_prs_with_shared_head_have_distinct_status_targets(self):
        self.report()
        first = list(self.fixture.write_routes)
        self.setUp()
        base, merge = "d" * 40, "e" * 40
        self.fixture.pr.update(number=4, merge_commit_sha=merge)
        self.fixture.pr["base"].update(ref="next", sha=base)
        self.run["pull_requests"] = [{"number": 4, "base": {"sha": base}}]
        self.run["referenced_workflows"] = [{"path": f"test/cli/{publisher.SOURCE_WORKFLOW}@{merge}",
                                               "ref": "refs/pull/4/merge", "sha": merge}]
        self.responses["actions/runs/42/attempts/2"] = dict(self.run)
        self.responses["pulls/4"] = self.fixture.pr
        self.responses["git/ref/pull/4/merge"] = {"ref": "refs/pull/4/merge", "object": {"sha": merge}}
        self.responses["git/commits/" + merge] = {"parents": [{"sha": base}, {"sha": HEAD}]}
        self.report()
        self.assertEqual(first, [f"statuses/{MERGE}"] * 6)
        self.assertEqual(self.fixture.write_routes, [f"statuses/{merge}"] * 6)

    def test_wrong_workflow_cannot_publish_ci_statuses(self):
        self.run["path"] = publisher.WORKFLOW
        with self.assertRaises(RuntimeError):
            self.report()
        self.assertEqual(self.fixture.writes, [])

    def test_invalid_newest_source_clears_prior_success_but_stale_run_cannot(self):
        self.report()
        self.fixture.writes.clear()
        self.run["referenced_workflows"] = []
        with self.assertRaises(RuntimeError):
            self.report()
        self.assertEqual([p["state"] for p in self.fixture.writes], ["pending"] * 3 + ["failure"] * 3)
        self.fixture.writes.clear()

        def get(route, **kwargs):
            if "actions/workflows/ci.yml/runs?" in route:
                return {"workflow_runs": [dict(self.run, id=43)]}
            return self.fixture.get(route, **kwargs)

        with self.assertRaises(publisher.StaleRun):
            self.report(get)
        self.assertEqual(self.fixture.writes, [])


if __name__ == "__main__":
    unittest.main()
