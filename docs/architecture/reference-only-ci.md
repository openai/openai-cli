# Reference-only CI shadow observations

The `Reference-only shadow report` PR job records committed path scope while
every existing check continues to run. It does not select jobs, validate schemas,
approve changes, or establish that a check is redundant. No CI saving is claimed.

## Consumer inventory

| Consumer | Reference dependency | Implication |
| --- | --- | --- |
| [`scripts/mock`](../../scripts/mock) | Loads `api_reference/openapi.transformed.yml`; checks its hash against `.castiron.stats.yml` before starting Steady. | Reference changes affect the mock contract. Keep mock-backed tests. |
| [`scripts/test`](../../scripts/test) | Starts the local mock by default, then runs Go tests and Windows test compilation. | Keep full test validation in shadow mode. |
| [`api_reference/go.mod`](../../api_reference/go.mod) | Separate Go module; reference files are outside the root Go module's package enumeration. | A module change is outside the allowlist and requires full validation. |
| [`cmd/`](../../cmd), [`pkg/`](../../pkg), [`internal/`](../../internal) | No direct transformed-spec reader found in the inventory at `ba6cb091387803581722e99443d8f3bfc59f9326`. Generated command behavior is committed Go code. | This is static evidence, not proof that all transitive dependencies are absent. |
| [GoReleaser](../../.goreleaser.yml) and [CI artifact builds](../../.github/workflows/ci.yml) | Build the CLI; generate completion/manpage assets by running it. The archive configuration does not list the reference spec. | Packaging is a candidate for later investigation, not a check this change skips. |
| [Native help compatibility](../../.github/workflows/help-compatibility.yml) | Exercises Go helpers and CLI behavior across Linux, macOS and Windows. | All platforms remain covered. |
| Castiron metadata/custom-code checks | Generation metadata records provenance and generated/custom-code accounting. | Metadata-only does not mean harmless; keep existing checks. |

Inventory method: search direct `api_reference`, `openapi.transformed` and
`go:embed` consumers, inspect test/mock entrypoints, nested module boundaries,
workflow commands and release archive inputs. Revisit this inventory when any
consumer changes. Reference-only describes paths, not semantic API impact.

## Report contract and trust

[`reference_shadow.py`](../../scripts/ci/reference_shadow.py) reports:

- `reference_only`: only text modifications to the existing regular files
  `api_reference/openapi.transformed.yml` and optionally `.castiron.stats.yml`.
- `metadata_only`: only text modifications to the existing metadata file.
- `full_ci`: any other path, addition, deletion, rename, mode/type change or
  binary change.
- `no_change`: empty committed diff; not a reference-only observation.
- `unknown`: invalid revisions, missing history, failed Git reads or a mismatch
  between event identity and the prepared merge checkout.

Every result has `shadow_only: true` and `full_ci_required: true`. In PR mode,
the prepared commit must have exactly the event base and PR head as its ordered
parents, and must be the checkout's HEAD. The diff is base to prepared merge.
Source preparation retains depth-two history so both parent trees are available;
this adds fetch/artifact overhead that should be measured with the shadow job.

The job runs candidate code with `permissions: {}` and no injected secrets.
Its report is advisory and candidate-controlled, not a trusted gate or selector.
It publishes a summary and a 14-day JSON artifact containing revisions, scope,
reason and file count, without copying filenames or reference contents. No other
job consumes it. A failed job or missing artifact counts as **unknown**, never
as evidence that fewer checks are safe. The advisory job may fail without
changing existing required checks. Push and merge-group events still run all
existing validation; they do not emit this PR-only report.

## Evidence and next decision

Local retrospective classification against each commit's first parent:

| Merged PR | Commit | Classification | Changed files |
| --- | --- | --- | --- |
| [#431](https://github.com/openai/openai-cli/pull/431) | `729045e` | reference_only | 2 |
| [#432](https://github.com/openai/openai-cli/pull/432) | `47df976` | reference_only | 2 |
| [#433](https://github.com/openai/openai-cli/pull/433) | `4f1f7df` | reference_only | 2 |
| [#434](https://github.com/openai/openai-cli/pull/434) | `2b61ced` | reference_only | 2 |
| [#414](https://github.com/openai/openai-cli/pull/414) | `835de59` | full_ci | 2 |
| [#435](https://github.com/openai/openai-cli/pull/435) | `ba6cb09` | full_ci | 7 |

These six local checks confirm path distinctions, not agreement with hosted
validation or a prospective shadow cohort. Before proposing a narrower lane,
review at least ten representative PRs against full CI, including mixed source,
metadata, dependency and reference changes. Record exact revisions, report,
full-check outcomes, retries, source-preparation overhead and elapsed critical
path; explain every mismatch and retain receipts before artifacts expire.
Separate private and public runs and check merge-queue behavior. A later,
separately reviewed change must establish the necessary consumer checks and
trusted selection boundary before any job can be skipped.

Run the focused tests with:

```sh
python3 -m unittest discover -s scripts/ci -p 'test_reference_shadow.py'
```
