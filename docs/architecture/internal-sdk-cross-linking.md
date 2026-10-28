# Internal Go SDK cross-linking

Same-repository pull requests into `main` in `openai/openai-cli-internal`
automatically use the same-named branch of `openai/openai-go-internal` when it
exists. Publish the Go branch before opening or updating the CLI pull request.
For example, both repositories can use `apcha/my-feature` to validate an
API that is not yet available in a released Go SDK.

The `Prepare Go SDK` workflow runs trusted tooling from `main`. Its isolated
fetch job uses the main-only `sdk-cross-link` environment and requests a
short-lived App token restricted to reading the Go repository. It downloads
source without executing either feature branch, then shares a source artifact
with ordinary PR checks. The artifact identifies the PR event, CLI commit, and
resolved Go commit.

A separate preparation job checks out the CLI as data, downloads the verified
SDK artifact, and packages both for its workflow run. It executes only reviewed
workflow instructions and SDK download tooling from `main`, never a candidate
script or local action. Its repository token cannot read the Go repository.
Build, test, help, and CodeQL analysis jobs have `permissions: {}`: they restore
these fixed inputs through same-run artifact access, without a repository-read
token or App credential. Their job
summaries show the selected Go commit, and their local module replacements are
never committed.

Cross-linking applies to CI, help compatibility, and Go CodeQL analysis.
CodeQL analyzes candidate code without repository permissions and stages SARIF
without uploading it. A separate `workflow_run` job runs the publisher from
trusted `main`, verifies the source run, attempt, current PR merge commit or
branch, and fixed language categories, then uploads reports through GitHub's API.
It never checks out candidate code or executes artifact contents.

The required `CodeQL (actions)` and `CodeQL (go)` statuses belong to this trusted
publisher. They remain pending while analysis or publication is outstanding,
fail if analysis or report processing fails, and succeed only after both reports
have been processed. Analysis jobs have distinct `CodeQL analysis (...)` names. On a partial rerun,
the publisher verifies each language's latest executed job and uses the artifact
from that successful attempt; a missing report from a newer execution cannot be
replaced by an older report.
Stale results cannot complete a newer revision's statuses. The publisher becomes
active after it lands on `main`; internal required statuses remain pending until
that rollout completes.

Workflow definitions themselves remain trusted configuration subject to workflow
change approval. This isolation protects against executable CLI/SDK code; it does
not sandbox someone authorized to rewrite workflows and grant new permissions.
Candidate jobs can read their selected source snapshot and use the current run's
artifact service. The snapshot is deliberately shared with those builds; it is
not confidential from candidate code. They cannot use a repository-read token to
fetch other branches, history, or artifacts from other runs.

The publisher confines publication authority to the verified revision. It does
not attest that candidate-produced SARIF faithfully represents the analysis:
candidate builds can influence their own reports. Protecting report integrity
against malicious build code requires a separate analysis design.

If the Go branch does not exist, checks explicitly use the committed released
dependency. Authentication and download failures do not fall back. If checks
time out waiting for source, inspect `Prepare Go SDK` first. Public and fork
pull requests, main pushes, merge queues, and releases keep their existing
dependency behavior.

Only snapshots from successful producer attempts are reused. A later failed
rerun does not invalidate an earlier successful snapshot; a producer rerun
replaces an artifact whose original attempt failed after uploading it.

Rerunning consumer checks reuses the snapshot for that PR event while its
artifact remains available (seven days). To select an updated Go branch,
recover from an expired artifact, or publish a Go branch that was initially
missing, update the CLI branch or close and reopen its PR. A Go-only push does
not automatically rerun CLI checks. The trusted workflow must first be merged
into `main` before its credential flow can be tested on an internal PR.

## Rollout order

Land the trusted producer first, and wait for public `main` to mirror into
internal `main` before enabling the consumer action in a follow-up PR.
`pull_request_target` can only prepare artifacts once its workflow exists on
internal `main`; enabling consumers in the first internal PR would leave them
waiting for an artifact that cannot yet be created. The producer-only stage
leaves existing build and test jobs unchanged.
