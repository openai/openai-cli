# Search vector stores

Search uses the existing vector-store API. A successful search can return no matches:

```sh
openai vector-stores search --vector-store-id vs_demo --query hello
```

```text
No matches returned.
Indexing state was not checked.
```

This message does not indicate indexing failure. Search does not make an additional status request.
Inspect processing separately before searching newly attached files:

```sh
openai vector-stores retrieve --vector-store-id vs_demo
openai vector-stores files retrieve --vector-store-id vs_demo --file-id file_demo
```

Wait for file processing to complete. A failed file exposes `last_error`; inspect it before retrying.
An empty result can also follow filters or ranking choices. Adjust those only when appropriate for your search.

```sh
openai vector-stores search --vector-store-id vs_demo --query hello \
  --filters '{"type":"eq","key":"category","value":"guide"}'
openai vector-stores search --vector-store-id vs_demo --query hello --format json
openai vector-stores search --vector-store-id vs_demo --query hello --format raw
```

Readable results retain returned scores, order, file IDs, filenames, attributes, and content chunks.
Explicit formats and field extraction retain their existing behavior. `raw` returns the API page envelope.
`--max-items 0` suppresses results. `--max-items -1` retains all items exposed by the existing search iterator.

The CLI does not provide a complete vector-store association lookup or establish deletion safety.
Do not infer that an unreferenced store is safe to delete from a partial consumer list.

Local fixtures verify CLI behavior, not live indexing acceptance.
See the [synthetic recording recipe](../../scripts/demos/vector-store-search/README.md).

## Live acceptance recipe

This recipe uploads one synthetic text file and creates one temporary vector store.
Run it only with authorization for live API usage and possible charges.
Use an approved test project with `OPENAI_API_KEY` already configured in the environment.
Keep the created resources exclusive to this run until cleanup finishes.

Prerequisites: `openai` on `PATH` and Python 3. No Python packages are required.
The script invokes public CLI commands. Python supplies JSON checks, subprocess timeouts, and bounded polling.
Every request uses `--format raw`, which preserves the API envelope independently of JSON list presentation.
The upload purpose `assistants` remains supported by the [Retrieval examples](https://developers.openai.com/api/docs/guides/retrieval).
This recipe does not create an Assistant.

The recipe waits up to 120 seconds for indexing, with a 30-second maximum per CLI call.
It stops on `failed`, `cancelled`, unexpected status, or timeout.
It reports `last_error` for failed processing and retains all response files for inspection.
It searches the exact synthetic sentence with a matching attribute filter, then checks an excluded filter.

```sh
python3 <<'PY'
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time

evidence = Path(tempfile.mkdtemp(prefix="openai-vector-acceptance-"))
source = evidence / "f15-acceptance.txt"
sentence = "The copper kite has seven blue ribbons."
source.write_text(sentence + "\n", encoding="utf-8")
attributes = {"category": "f15-acceptance", "revision": 7, "published": True}
file_id = store_id = None
accepted = False
cleanup_failed = False
print(f"Response files: {evidence}", flush=True)

def cli(label, *arguments, timeout=30):
    response = evidence / (label + ".json")
    with response.open("wb") as output:
        subprocess.run(
            ["openai", "--format", "raw", *arguments],
            stdin=subprocess.DEVNULL, stdout=output, check=True, timeout=timeout,
        )
    return json.loads(response.read_bytes())

def require(condition, message):
    if not condition:
        raise RuntimeError(message)

try:
    uploaded = cli("upload", "files", "create", "--file", str(source),
                   "--purpose", "assistants")
    file_id = uploaded["id"]
    store = cli("store", "vector-stores", "create",
                "--name", "f15-acceptance-" + evidence.name,
                "--expires-after", '{"anchor":"last_active_at","days":1}')
    store_id = store["id"]
    cli("attach", "vector-stores", "files", "create",
        "--vector-store-id", store_id, "--file-id", file_id,
        "--attributes", json.dumps(attributes))

    deadline = time.monotonic() + 120
    attempt = 0
    while True:
        remaining = deadline - time.monotonic()
        require(remaining > 0, "Indexing timed out; inspect the latest status response.")
        attempt += 1
        status = cli(f"status-{attempt:03d}", "vector-stores", "files", "retrieve",
                     "--vector-store-id", store_id, "--file-id", file_id,
                     timeout=min(30, remaining))
        state = status["status"]
        if state == "completed":
            break
        require(state == "in_progress",
                "Indexing stopped: " + json.dumps({
                    "status": state, "last_error": status.get("last_error")
                }))
        time.sleep(min(2, max(0, deadline - time.monotonic())))

    for label, category in (("matching", "f15-acceptance"), ("excluded", "other")):
        result = cli(label, "vector-stores", "search", "--vector-store-id", store_id,
                     "--query", sentence, "--rewrite-query=false",
                     "--max-num-results", "10", "--ranking-options.score-threshold", "0",
                     "--filters", json.dumps({"type": "eq", "key": "category", "value": category}))
        rows = result["data"]
        require(result["has_more"] is False, "Unexpected additional results; inspect the raw envelope.")
        if label == "excluded":
            require(rows == [], "The excluded filter returned results.")
            continue
        require(bool(rows), "The matching filter returned no results.")
        for row in rows:
            require(row["file_id"] == file_id, "Unexpected file ID.")
            require(row["filename"] == source.name, "Unexpected filename.")
            actual = row["attributes"]
            require(actual == attributes and actual.get("published") is True
                    and type(actual.get("revision")) in (int, float),
                    "Attributes or their JSON types changed.")
            require(any(chunk.get("type") == "text" and sentence in chunk.get("text", "")
                        for chunk in row["content"]), "The synthetic sentence is missing.")
    accepted = True
except (Exception, KeyboardInterrupt) as error:
    print(f"Acceptance failed: {error}", file=sys.stderr)
finally:
    # Only IDs returned by this run can reach these delete commands.
    for label, resource_id, arguments in (
        ("delete-store", store_id, ("vector-stores", "delete", "--vector-store-id", store_id)),
        ("delete-file", file_id, ("files", "delete", "--file-id", file_id)),
    ):
        if not resource_id:
            continue
        try:
            deleted = cli(label, *arguments)
            require(deleted.get("id") == resource_id and deleted.get("deleted") is True,
                    "Deletion was not confirmed.")
        except Exception as error:
            cleanup_failed = True
            print(f"Cleanup failed for {resource_id}: {error}", file=sys.stderr)

if not accepted or cleanup_failed:
    raise SystemExit(1)
print("Acceptance checks and cleanup passed.")
PY
```

The recipe deletes the temporary store first. It separately deletes the uploaded source file.
Deleting a vector-store attachment only removes that attachment; it does not delete the source file.
See [Delete vector store file](https://developers.openai.com/api/reference/resources/vector_stores/subresources/files/methods/delete).
Deleting a source file removes it from all vector stores, as documented in [Delete file](https://developers.openai.com/api/reference/resources/files/methods/delete).
Use only the IDs returned by this run. Do not substitute existing resources or infer deletion safety from consumer lists.

A timeout or interruption can leave resources behind. A timed-out creation can succeed without returning its ID.
Inspect the saved responses and test project before retrying. Do not use bulk cleanup.
The store expiration policy does not replace source-file cleanup or guarantee zero charges.
Retain the CLI version, response files, observed status, and exit status as acceptance evidence.
An unexecuted recipe or a local mock run does not establish live acceptance.
