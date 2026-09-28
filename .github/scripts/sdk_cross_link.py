"""Prepare private Go source in trusted CI without executing candidate code."""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import urllib.error
import urllib.parse
import urllib.request
import zipfile


REPOSITORY = "openai/openai-cli-internal"
REPOSITORY_ID = 1253662577
GO_REPOSITORY = "openai/openai-go-internal"
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


def existing_snapshot(api, context, run_id):
    # A rerun retains the run ID and immutable artifacts from earlier attempts.
    # Reuse that source instead of moving the Go revision or colliding on upload.
    result = api.get(f"repos/{REPOSITORY}/actions/runs/{run_id}/artifacts?per_page=100")
    for artifact in result["artifacts"]:
        if artifact["name"] == artifact_name(context) and not artifact["expired"]:
            data = api.get(f"repos/{REPOSITORY}/actions/artifacts/{artifact['id']}/zip", binary=True)
            unpack_artifact(data, context, run_id)
            return True
    return False


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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["produce"])
    parser.parse_args()
    if os.environ["GITHUB_EVENT_NAME"] != "pull_request_target":
        raise RuntimeError("Unexpected workflow event")
    if os.environ["GITHUB_REF"] != "refs/heads/main":
        raise RuntimeError("Producer must run from main")
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    context = identity(event)
    api = GitHub(os.environ.get("GH_TOKEN"))
    run_id = int(os.environ["GITHUB_RUN_ID"])
    reused = existing_snapshot(api, context, run_id)
    name = artifact_name(context) if reused else produce(
        event, api, GitHub(os.environ.get("SDK_TOKEN")),
        Path(os.environ["RUNNER_TEMP"]) / "go-sdk-artifact",
        run_id, int(os.environ["GITHUB_RUN_ATTEMPT"]))
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"artifact-name={name}\n")
        output.write(f"upload={'false' if reused else 'true'}\n")


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
