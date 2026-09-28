"""Fetch private Go source in trusted CI; consume it in ordinary PR jobs.

The producer never extracts or executes either candidate. Only the consumer
runs Go, and it runs without the App credential or credential environment.
"""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile


REPOSITORY = "openai/openai-cli-internal"
REPOSITORY_ID = 1253662577
GO_REPOSITORY = "openai/openai-go-internal"
MODULE = "github.com/openai/openai-go/v3"
WORKFLOW = ".github/workflows/sdk-cross-link.yml"
SHA = re.compile(r"[0-9a-f]{40}")


class APIError(RuntimeError):
    def __init__(self, status):
        self.status = status
        super().__init__(f"GitHub request failed (HTTP {status})")


class DownloadRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != "https":
            raise RuntimeError("Refusing a non-HTTPS download redirect")
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        # Archive downloads redirect to signed storage URLs. Never forward the
        # GitHub credential, or print the signed destination in diagnostics.
        redirected.remove_header("Authorization")
        return redirected


class GitHub:
    def __init__(self, token):
        if not token:
            raise RuntimeError("Missing GitHub token")
        self.token = token

    def get(self, route, binary=False):
        if not route.startswith("repos/"):
            raise RuntimeError("Unexpected GitHub API route")
        request = urllib.request.Request(
            "https://api.github.com/" + route,
            headers={"Authorization": "Bearer " + self.token,
                     "Accept": "application/vnd.github+json",
                     "X-GitHub-Api-Version": "2022-11-28"},
        )
        try:
            with urllib.request.build_opener(DownloadRedirect()).open(request, timeout=30) as response:
                data = response.read()
        except urllib.error.HTTPError as error:
            raise APIError(error.code) from None
        except (urllib.error.URLError, TimeoutError):
            raise RuntimeError("GitHub download failed or timed out") from None
        return data if binary else json.loads(data)


def identity(event):
    repo = event["repository"]
    pr = event["pull_request"]
    if (repo["id"] != REPOSITORY_ID or repo["full_name"] != REPOSITORY or
            not repo["private"] or pr["head"]["repo"]["id"] != REPOSITORY_ID or
            pr["base"]["repo"]["id"] != REPOSITORY_ID or pr["base"]["ref"] != "main"):
        raise RuntimeError("Cross-linking is limited to same-repository internal PRs into main")
    head = pr["head"]["sha"]
    if not SHA.fullmatch(head):
        raise RuntimeError("Invalid CLI commit")
    return {"repository_id": REPOSITORY_ID, "pr": event["number"],
            "head_sha": head, "branch": pr["head"]["ref"],
            "updated_at": pr["updated_at"]}


def artifact_name(context):
    # Both pull_request and pull_request_target receive the same PR payload.
    # Reopening a PR produces a new identity; a rerun reuses the same snapshot.
    digest = hashlib.sha256(json.dumps(context, sort_keys=True).encode()).hexdigest()
    return "go-sdk-" + digest


def produce(event, cli, sdk, destination, run_id, attempt):
    context = identity(event)
    current = cli.get(f"repos/{REPOSITORY}/pulls/{context['pr']}")
    if (current["state"] != "open" or current["head"]["sha"] != context["head_sha"] or
            current["head"]["ref"] != context["branch"] or
            current["head"]["repo"]["id"] != REPOSITORY_ID or
            current["base"]["ref"] != "main"):
        raise RuntimeError("PR changed before the Go source could be prepared")
    # Establish repository access before interpreting a missing ref as fallback.
    sdk.get(f"repos/{GO_REPOSITORY}")
    branch = urllib.parse.quote(context["branch"], safe="")
    manifest = {"schema": 1, "context": context, "run_id": run_id,
                "run_attempt": attempt, "go_repository": GO_REPOSITORY}
    try:
        ref = sdk.get(f"repos/{GO_REPOSITORY}/git/ref/heads/{branch}")
    except APIError as error:
        if error.status != 404:
            raise
        manifest["mode"] = "released"
    else:
        sha = ref["object"]["sha"]
        if ref["ref"] != "refs/heads/" + context["branch"] or not SHA.fullmatch(sha):
            raise RuntimeError("Unexpected Go branch response")
        archive = sdk.get(f"repos/{GO_REPOSITORY}/tarball/{sha}", binary=True)
        destination.mkdir(parents=True, exist_ok=True)
        (destination / "sdk.tar.gz").write_bytes(archive)
        manifest.update(mode="linked", go_sha=sha,
                        archive_sha256=hashlib.sha256(archive).hexdigest())
    destination.mkdir(parents=True, exist_ok=True)
    (destination / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    return artifact_name(context)


def trusted_run(run, context):
    # REST run metadata identifies the PR head, even though pull_request_target
    # executes the base workflow. Bind both that head and the artifact manifest.
    return (run["event"] == "pull_request_target" and run["path"] == WORKFLOW and
            run["repository"]["id"] == REPOSITORY_ID and
            run["head_repository"]["id"] == REPOSITORY_ID and
            run["head_branch"] == context["branch"] and run["head_sha"] == context["head_sha"])


def successful_attempt(api, context, run_id, attempt):
    if type(attempt) is not int or attempt < 1:
        raise RuntimeError("Invalid Go source workflow attempt")
    original = api.get(f"repos/{REPOSITORY}/actions/runs/{run_id}/attempts/{attempt}")
    return (trusted_run(original, context) and original["run_attempt"] == attempt and
            original["status"] == "completed" and original["conclusion"] == "success")


def existing_snapshot(api, context, run_id):
    # A rerun retains the run ID and immutable artifacts from earlier attempts.
    # Reuse that source instead of moving the Go revision or colliding on upload.
    result = api.get(f"repos/{REPOSITORY}/actions/runs/{run_id}/artifacts?per_page=100")
    for artifact in result["artifacts"]:
        if artifact["name"] == artifact_name(context) and not artifact["expired"]:
            data = api.get(f"repos/{REPOSITORY}/actions/artifacts/{artifact['id']}/zip", binary=True)
            manifest, _ = unpack_artifact(data, context, run_id)
            return successful_attempt(api, context, run_id, manifest["run_attempt"])
    return False


def wait_for_artifact(api, context, timeout=300, sleep=time.sleep, clock=time.monotonic):
    deadline = clock() + timeout
    name = artifact_name(context)
    while True:
        artifacts = api.get(f"repos/{REPOSITORY}/actions/artifacts?name={name}&per_page=100")
        # The first successful producer for this event fixes the snapshot for
        # every consumer, even if the producer is subsequently rerun.
        for artifact in sorted(artifacts["artifacts"], key=lambda item: item["id"]):
            if artifact["name"] != name or artifact["expired"]:
                continue
            run_id = artifact["workflow_run"]["id"]
            run = api.get(f"repos/{REPOSITORY}/actions/runs/{run_id}")
            if not trusted_run(run, context):
                continue
            try:
                data = api.get(f"repos/{REPOSITORY}/actions/artifacts/{artifact['id']}/zip", binary=True)
            except APIError as error:
                if error.status != 404:
                    raise
                # A rerun can replace an unusable artifact after it was listed.
                continue
            manifest, archive = unpack_artifact(data, context, run_id)
            if not successful_attempt(api, context, run_id, manifest["run_attempt"]):
                continue
            return manifest, archive
        if clock() >= deadline:
            raise RuntimeError("Timed out waiting for the trusted Go source; inspect the Prepare Go SDK workflow")
        sleep(5)


def unpack_artifact(data, context, run_id):
    with zipfile.ZipFile(io.BytesIO(data)) as artifact:
        names = artifact.namelist()
        if len(names) != len(set(names)) or set(names) not in (
                {"manifest.json"}, {"manifest.json", "sdk.tar.gz"}):
            raise RuntimeError("Unexpected Go source artifact files")
        manifest = json.loads(artifact.read("manifest.json"))
        if (manifest["schema"] != 1 or manifest["context"] != context or
                manifest["run_id"] != run_id or manifest["go_repository"] != GO_REPOSITORY):
            raise RuntimeError("Go source artifact does not match this PR event")
        if manifest["mode"] == "released" and names == ["manifest.json"]:
            return manifest, None
        if manifest["mode"] != "linked" or not SHA.fullmatch(manifest["go_sha"]):
            raise RuntimeError("Invalid Go source revision")
        archive = artifact.read("sdk.tar.gz")
        if hashlib.sha256(archive).hexdigest() != manifest["archive_sha256"]:
            raise RuntimeError("Go source archive checksum mismatch")
        return manifest, archive


def extract_source(data, destination):
    """Extract regular source files under a fresh directory on every platform."""
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        seen = set()
        root = None
        for member in archive:
            path = PurePosixPath(member.name)
            if (path.is_absolute() or ".." in path.parts or "\\" in member.name or
                    ":" in member.name or not path.parts or
                    not (member.isdir() or member.isfile())):
                raise RuntimeError("Unsafe Go source archive entry")
            if root is None:
                root = path.parts[0]
            if path.parts[0] != root:
                raise RuntimeError("Go source archive has multiple roots")
            relative = Path(*path.parts[1:])
            key = relative.as_posix().casefold()
            if key in seen:
                raise RuntimeError("Duplicate Go source archive entry")
            seen.add(key)
            target = destination / relative
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, target.open("xb") as output:
                    shutil.copyfileobj(source, output)
    if not (destination / "go.mod").is_file():
        raise RuntimeError("Go source archive has no module")


def link_source(source, workspace):
    env = {key: value for key, value in os.environ.items() if key not in ("GH_TOKEN", "SDK_TOKEN")}
    module = subprocess.run(["go", "mod", "edit", "-json"], cwd=source, env=env,
                            check=True, capture_output=True, text=True)
    if json.loads(module.stdout)["Module"]["Path"] != MODULE:
        raise RuntimeError("Unexpected Go SDK module path")
    subprocess.run(["go", "mod", "edit", f"-replace={MODULE}={source}"],
                   cwd=workspace, env=env, check=True)
    subprocess.run(["go", "mod", "tidy"], cwd=workspace, env=env, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["produce", "consume"])
    args = parser.parse_args()
    expected_event = "pull_request_target" if args.mode == "produce" else "pull_request"
    if os.environ["GITHUB_EVENT_NAME"] != expected_event:
        raise RuntimeError("Unexpected workflow event")
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    context = identity(event)
    api = GitHub(os.environ.get("GH_TOKEN"))
    if args.mode == "produce":
        if os.environ["GITHUB_REF"] != "refs/heads/main":
            raise RuntimeError("Producer must run from main")
        run_id = int(os.environ["GITHUB_RUN_ID"])
        reused = existing_snapshot(api, context, run_id)
        name = artifact_name(context) if reused else produce(
            event, api, GitHub(os.environ.get("SDK_TOKEN")),
            Path(os.environ["RUNNER_TEMP"]) / "go-sdk-artifact",
            run_id, int(os.environ["GITHUB_RUN_ATTEMPT"]))
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write(f"artifact-name={name}\n")
            output.write(f"upload={'false' if reused else 'true'}\n")
        return
    manifest, archive = wait_for_artifact(api, context)
    if archive is None:
        summary = "No matching Go SDK branch; using the committed released dependency."
    else:
        source = Path(tempfile.mkdtemp(prefix="go-sdk-", dir=os.environ["RUNNER_TEMP"]))
        extract_source(archive, source)
        link_source(source, Path(os.environ["GITHUB_WORKSPACE"]))
        summary = f"Cross-linked Go SDK commit: {manifest['go_sha']}"
    print(summary)
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as output:
        output.write(summary + "\n")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # Do not include URLs, HTTP response bodies, or candidate-controlled
        # archive names in exception diagnostics.
        if type(error) in (RuntimeError, APIError):
            print(str(error), file=__import__("sys").stderr)
        else:
            print(f"Go SDK cross-link failed ({type(error).__name__})", file=__import__("sys").stderr)
        raise SystemExit(1)
