# Limits in the terminal

The limits workspace manages the controls exposed by the public administration API.
It requires an admin API key through the existing CLI authentication settings.

```sh
openai admin limits
openai projects limits --project-id proj_example
```

An interactive terminal opens the workspace.
Pipes, CI, quiet mode, explicit output formats, and extraction print a read-only overview.
The `show` command always prints the overview.

```sh
openai admin limits show
openai projects limits show --project-id proj_example --format json
openai admin limits show --max-items -1
openai admin limits show --format json --transform spend_alerts
```

`--max-items` defaults to 100 records per list.
Use `-1` for all records or `0` to validate the list request without retaining records.
The interactive workspace puts each list's Complete or Incomplete status before its loaded count.
Failed reads preserve available data and return a nonzero exit status.
Cancellation can stop output before the CLI prints those records.
The `source_errors` field identifies failed sources without exposing response error bodies.
Machine formats and extraction retain original resource fields and numeric values.

## Current spend

The overview includes reported current-month Costs with UTC daily boundaries.
Today’s costs remain partial and can arrive later.
Costs amounts use currency units; spend-limit amounts use integer cents.
The CLI preserves exact decimal totals and original buckets.
Incomplete Costs totals cover only returned buckets.
Check `costs.complete` before treating a total as the complete reported period.
Ambiguous Costs data produces no total.
Costs data cannot establish authoritative enforcement state.
The derived summary supports up to 10,000 decimal digits.
Values beyond that precision remain available in `costs.buckets`; the summary reports its limitation.

## Workspace controls

| Control | Action |
| --- | --- |
| Left / Right | Select Spend, Alerts, or project Rates |
| Up / Down | Select a record or scroll a review |
| Enter | Open the selected action |
| Tab / Shift+Tab | Move through form fields or confirmation actions |
| r | Refresh the overview or a rejected draft |
| i | Inspect the selected original API record |
| ? | Show workspace help and API boundaries |
| F1 | Show help, including complete form diagnostics |
| d | Delete the selected alert |
| Escape | Return without saving |
| q | Close the overview |
| Ctrl+P | Print the reviewed command and exit without saving |
| Ctrl+C | Interrupt from any state |

The workspace needs at least 40 columns and 12 rows.
Smaller terminals retain cancellation and block submission.
The workspace restores the terminal screen when it exits.
Escape closes help and restores the active form or review.
Forms retain their fields, focus, and subject-prefix mode.
Repeated F1 presses preserve that return point.
Escape from a review returns to the form, like Back.

Every change opens a before-and-after review.
Spend and alert reviews show exact monthly USD amounts alongside the original API fields.
Scroll to the end before selecting Apply.
Back is the initial selection, including after resizing.
Returning from help also selects Back.
Resizing during help requires rereading the review before selecting Apply.
The review shows a shell command when the CLI can reproduce the request context safely.
Wrapped command rows are for inspection only.
Press Ctrl+P to print the complete command after the terminal screen restores.
Copy the printed command when you want to run it later.
Printing sends no change and preserves any earlier confirmed receipt.
The workspace omits command previews that require private options or unsupported shells.
Existing administration commands remain available for scripts.

```sh
# Existing scriptable commands use integer cents.
openai admin spend-limit update --currency USD --interval month --threshold-amount 12550
openai admin spend-alerts list --max-items -1
openai projects rate-limits list-rate-limits --project-id proj_example
openai admin usage costs --help
```

## Supported changes

Spend supports setting, replacing, and removing hard monthly USD limits.
Enter decimal dollars, such as `125.50`.
The CLI converts dollars to integer cents without floating-point arithmetic.
A hard limit must be at least USD 0.01.
Enforcement can be delayed after the threshold is reached.
Removing a hard limit removes that limit’s enforcement.
Parent limits can still apply.
Use the Platform website to inspect or edit any retained soft budget.

Alerts support creation, editing, and deletion.
Alerts notify recipients; they do not enforce a cap.
Enter recipients as comma-separated email addresses.
Subject prefixes can preserve the original value, replace it, or clear it to null.
An empty replacement remains an empty string.
The workspace does not require a hard limit before creating an alert.
The API still validates account-specific constraints.

Project Rates supports seven named rate fields, including tokens per day.
The form sends only changed fields.
Blank fields remain unchanged.
Zero has no assumed unlimited meaning.
The workspace accepts nonnegative integers for fields returned as integers.
The workspace skips missing and null fields.
Sparse records can expose only daily limits.
Fine-tuned `ft:` models and `-shared` records remain available for inspection.
The API decides account-specific caps.
Scriptable commands retain their existing input behavior.
Rate updates require a complete preflight list; use `--max-items -1` when necessary.

The named tokens-per-day flag extends an existing API field:

```sh
openai projects rate-limits update-rate-limit \
  --project-id proj_example --rate-limit-id rl_example \
  --max-tokens-per-1-day 100000
```

JSON and YAML stdin can also send `max_tokens_per_1_day`.
The workspace verifies the returned model and every requested value before confirming success.
A mismatched confirmation locks further writes and directs you to inspect current settings.

## Compact terminal interface

The workspace uses an inline panel and preserves shell scrollback.
The panel uses up to 76 columns and 18 rows.
The minimum terminal size remains 40 columns and 12 rows.
Selection uses a fixed-width marker and keeps the same font weight.
Forms and details open through explicit actions.
Short details use only their content height.
Machine output and redirected output remain read-only.

## Concurrent changes and recovery

The CLI checks the selected record again before writing.
This check is not atomic and cannot prevent every concurrent change.
A changed baseline stops the write.
A 404 response leaves the current limit unconfirmed.
Setting a limit requires reviewing that uncertainty and explicitly approving creation or replacement.
Refresh the retained draft before reviewing it again.
If a refreshed rate field becomes unavailable, its edited draft remains in F1 details.
Select Discard unavailable edits to remove those drafts explicitly.
Other edits remain intact.
Ambiguous or unfamiliar replacement configuration remains available for inspection.
The form refuses edits that could discard that configuration.

The workspace never automatically retries a mutation.
Scriptable API commands retain their existing retry behavior.
A command preview does not reproduce workspace preflight checks or retry suppression.
An uncertain save locks further writes in that workspace session.
Inspect the resource before reopening the workspace or repeating the action.
A refresh alone cannot prove that an uncertain alert creation failed.
Cancellation does not prove that the server canceled a dispatched write.

## Fine-tuning model limits

The new `openai fine-tuning model-limits` command reads the documented fine-tuning model limits.
Use an ordinary `OPENAI_API_KEY`; this command does not use admin-key fallback.
Root organization, project, connection, and output options remain available.
The command reads one endpoint and preserves all response fields, including unknown fields.
It adds no pagination or mutation flags.

```sh
openai fine-tuning model-limits --format json
```

See the [fine-tuning rate-limits documentation](https://developers.openai.com/api/docs/guides/rate-limits#fine-tuning-rate-limits).

## Platform controls without matching contracts

The [Platform limits page](https://platform.openai.com/settings/organization/limits) includes controls beyond these public operations.
The inspected public contract does not expose the website's soft-budget and enforcement-toggle combination.
It also lacks the organization tier catalog, account upgrade operations, and approved usage-limit controls.
The workspace does not emulate these controls through undocumented requests.

Project rate records do not describe every organization-wide model limit or shared model-family limit.
Historical costs are not a live allowance or enforcement counter.
See the [spend-limits guide](https://developers.openai.com/api/docs/guides/spend-limits)
and [rate-limits guide](https://developers.openai.com/api/docs/guides/rate-limits) for API behavior.
