"""Publish candidate SARIF as data, using only GitHub-verified destinations."""

import base64
import gzip
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
from urllib.parse import quote
import zipfile

WORKFLOW = ".github/workflows/codeql.yml"
CATEGORIES = {
    "actions": WORKFLOW + ":analyze/build-mode:none/language:actions",
    "go": WORKFLOW + ":analyze/build-mode:autobuild/language:go",
}
SHA = re.compile(r"[0-9a-f]{40}")


class StaleRun(Exception):
    pass


def api(route, payload=None, binary=False, missing_ok=False):
    command = ["gh", "api", "--hostname", "github.com", route]
    data = None
    if payload is not None:
        command += ["--method", "POST", "--input", "-"]
        data = json.dumps(payload).encode()
    result = subprocess.run(command, input=data, capture_output=True, check=False)
    if result.returncode:
        if missing_ok and b"HTTP 404" in result.stderr:
            raise StaleRun("Source ref no longer exists")
        # Never print response bodies, signed artifact URLs, or credentials.
        raise RuntimeError("GitHub API request failed")
    return result.stdout if binary else json.loads(result.stdout)


def pages(get, route, key=None):
    separator = "&" if "?" in route else "?"
    for page in range(1, 101):
        result = get(f"{route}{separator}per_page=100&page={page}")
        items = result[key] if key else result
        yield from items
        if len(items) < 100:
            return
    raise RuntimeError("Too many GitHub results")


def destination(get, repository, repository_id, source):
    """Candidate metadata never selects the upload repository or destination."""
    root = f"repos/{repository}"
    run = get(f"{root}/actions/runs/{int(source['id'])}")
    if (run["id"] != source["id"] or run["run_attempt"] != source["run_attempt"]):
        raise StaleRun("Source workflow has been rerun")
    if (run["repository"]["id"] != repository_id or
            run["repository"]["full_name"] != repository or
            run["path"].split("@", 1)[0] != WORKFLOW or
            run["status"] != "completed" or run["conclusion"] != "success" or
            not SHA.fullmatch(run["head_sha"])):
        raise RuntimeError("Unexpected source workflow")
    if run["event"] == "pull_request":
        associated = run["pull_requests"]
        if not associated:
            associated = list(pages(get, f"{root}/commits/{run['head_sha']}/pulls"))
        if not associated:
            head = quote(run["head_repository"]["owner"]["login"] + ":" + run["head_branch"], safe="")
            associated = list(pages(get, f"{root}/pulls?state=open&base=main&head={head}"))
        candidates = []
        for number in sorted({int(p["number"]) for p in associated}):
            pr = get(f"{root}/pulls/{number}")
            if (pr["state"] == "open" and pr["head"]["sha"] == run["head_sha"] and
                    pr["head"]["ref"] == run["head_branch"] and
                    (pr["head"]["repo"] or {}).get("id") == run["head_repository"]["id"] and
                    pr["base"]["repo"]["id"] == repository_id and pr["base"]["ref"] == "main"):
                candidates.append(pr)
        if len(candidates) != 1:
            raise StaleRun("No unique current pull request for this run")
        pr = candidates[0]
        for recorded in run["pull_requests"]:
            if recorded["number"] == pr["number"] and recorded["base"]["sha"] != pr["base"]["sha"]:
                raise StaleRun("Pull request base changed")
        ref = f"refs/pull/{pr['number']}/merge"
        merge = get(f"{root}/git/ref/pull/{pr['number']}/merge", missing_ok=True)
        sha = merge["object"]["sha"]
        if merge["ref"] != ref or sha != pr["merge_commit_sha"] or not SHA.fullmatch(sha):
            raise StaleRun("Pull request merge changed")
        commit = get(f"{root}/git/commits/{sha}")
        if [p["sha"] for p in commit["parents"]] != [pr["base"]["sha"], run["head_sha"]]:
            raise StaleRun("Pull request merge parents changed")
    else:
        branch = run["head_branch"]
        if (run["head_repository"]["id"] != repository_id or
                not (run["event"] == "push" and branch == "main" or
                     run["event"] == "merge_group" and branch.startswith("gh-readonly-queue/main/"))):
            raise RuntimeError("Unexpected source event or branch")
        ref, sha = "refs/heads/" + branch, run["head_sha"]
        current = get(f"{root}/git/ref/heads/{quote(branch, safe='')}", missing_ok=True)
        if current["ref"] != ref or current["object"]["sha"] != sha:
            raise StaleRun("Source branch advanced")
    return run, {"sha": sha, "ref": ref}


def read_artifact(data, run, target, language):
    # Read fixed members in memory. Never extract or follow artifact paths.
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        if sorted(archive.namelist()) != ["context.json", "upload.sarif"]:
            raise RuntimeError("Unexpected SARIF artifact files")
        context = json.loads(archive.read("context.json"))
        expected = dict(target, run_id=run["id"], run_attempt=run["run_attempt"], language=language)
        if context != expected:
            raise StaleRun("SARIF does not describe the verified source revision")
        sarif = json.loads(archive.read("upload.sarif"))
    if sarif.get("version") != "2.1.0" or len(sarif.get("runs", [])) != 1:
        raise RuntimeError("Expected one SARIF analysis")
    analysis = sarif["runs"][0]
    if analysis["tool"]["driver"]["name"] != "CodeQL":
        raise RuntimeError("Expected CodeQL results")
    # These fields can affect attribution; candidate values have no authority.
    analysis["automationDetails"] = {"id": CATEGORIES[language] + "/"}
    analysis.pop("runAggregates", None)
    analysis.pop("versionControlProvenance", None)
    analysis["tool"]["driver"].pop("guid", None)
    return sarif


def publish(get, repository, repository_id, source, sleep=time.sleep):
    run, target = destination(get, repository, repository_id, source)
    root = f"repos/{repository}"
    artifacts = list(pages(get, f"{root}/actions/runs/{run['id']}/artifacts", "artifacts"))
    reports = []
    for language in CATEGORIES:
        name = f"codeql-sarif-{run['id']}-{run['run_attempt']}-{language}"
        matches = [a for a in artifacts if a["name"] == name and not a["expired"]]
        if len(matches) != 1:
            raise RuntimeError("Missing or duplicate SARIF artifact")
        data = get(f"{root}/actions/artifacts/{int(matches[0]['id'])}/zip", binary=True)
        reports.append(read_artifact(data, run, target, language))
    # Recheck after downloads, before the first write. Explicit SHA/ref remain
    # immutable even if the branch moves after this check.
    if destination(get, repository, repository_id, source)[1] != target:
        raise StaleRun("Source changed while downloading results")
    for report in reports:
        payload = {"commit_sha": target["sha"], "ref": target["ref"],
                   "sarif": base64.b64encode(gzip.compress(json.dumps(report).encode())).decode(),
                   "tool_name": "CodeQL"}
        uploaded = get(f"{root}/code-scanning/sarifs", payload=payload)
        identifier = uploaded["id"]
        if not re.fullmatch(r"[A-Za-z0-9-]+", identifier):
            raise RuntimeError("Unexpected SARIF processing identifier")
        for _ in range(60):
            result = get(f"{root}/code-scanning/sarifs/{identifier}")
            if result["processing_status"] == "complete":
                break
            if result["processing_status"] != "pending":
                raise RuntimeError("CodeQL result processing failed")
            sleep(5)
        else:
            raise RuntimeError("CodeQL result processing timed out")
    print(f"Published CodeQL results for {target['sha']}")


def main():
    if sys.argv[1:] == ["record"]:
        language = os.environ["CODEQL_LANGUAGE"]
        if language not in CATEGORIES:
            raise RuntimeError("Unexpected CodeQL language")
        sha = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
        context = {"sha": sha, "ref": os.environ["GITHUB_REF"], "language": language,
                   "run_id": int(os.environ["GITHUB_RUN_ID"]),
                   "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"])}
        (Path(os.environ["SARIF_DIRECTORY"]) / "context.json").write_text(json.dumps(context))
    elif sys.argv[1:] == ["publish"] and os.environ["GITHUB_EVENT_NAME"] == "workflow_run":
        event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        if event["repository"]["full_name"] != os.environ["GITHUB_REPOSITORY"]:
            raise RuntimeError("Unexpected event repository")
        publish(api, os.environ["GITHUB_REPOSITORY"], int(os.environ["GITHUB_REPOSITORY_ID"]),
                event["workflow_run"])
    else:
        raise RuntimeError("Unexpected uploader invocation")


if __name__ == "__main__":
    try:
        main()
    except StaleRun as error:
        print(f"Skipping stale CodeQL results: {error}")
    except Exception as error:
        print(f"CodeQL upload failed ({type(error).__name__})", file=sys.stderr)
        raise SystemExit(1)
