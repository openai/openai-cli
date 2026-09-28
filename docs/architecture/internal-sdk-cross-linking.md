# Internal Go SDK cross-linking

Same-repository pull requests into `main` in `openai/openai-cli-internal`
automatically use the same-named branch of `openai/openai-go-internal` when it
exists. Publish the Go branch before opening or updating the CLI pull request.
For example, both repositories can use `developer/my-feature` to validate an
API that is not yet available in a released Go SDK.

The `Prepare Go SDK` workflow runs trusted tooling from `main`. Its isolated
fetch job uses the main-only `sdk-cross-link` environment and requests a
short-lived App token restricted to reading the Go repository. It downloads
source without executing either feature branch, then shares a source artifact
with ordinary PR checks. The artifact identifies the PR event, CLI commit, and
resolved Go commit. Build and test jobs receive no App credentials. Their job
summaries show the selected Go commit, and their local module replacements are
never committed.

Cross-linking applies to CI, help compatibility, and Go CodeQL analysis.
CodeQL analyzes candidate code with a read-only token and stages SARIF without
uploading it. A separate `workflow_run` job runs the publisher from trusted
`main`, verifies the source run, attempt, current PR merge commit or branch,
and fixed language categories, then uploads the reports through GitHub's API.
It never checks out candidate code or executes artifact contents. Stale results
are skipped; processing failures fail the publisher.

This isolates the write token and constrains where reports can be published.
It does not attest that candidate-produced SARIF faithfully represents the
analysis: candidate workflows and builds can influence their own reports.
The publisher first becomes active after it lands on `main`; until then the
analysis jobs stage results but do not update code-scanning results.

If the Go branch does not exist, checks explicitly use the committed released
dependency. Authentication and download failures do not fall back. If checks
time out waiting for source, inspect `Prepare Go SDK` first. Public and fork
pull requests, main pushes, merge queues, and releases keep their existing
dependency behavior.

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
