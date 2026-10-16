# Project cost reports

`openai costs report` totals organization costs by project over an explicit date range.
It reads the public Costs API with your existing Admin key configuration.
Set `OPENAI_ADMIN_KEY` through your normal secret manager or environment.
An explicit `--admin-api-key` overrides `OPENAI_ADMIN_KEY`.
An explicit `--header` Authorization value overrides both, including when its value is empty.
For authentication failures with that header, correct or remove the header override first.
Otherwise, check the explicit Admin key when present, or its environment setting.
For permission failures, check the Admin key's access to organization costs and the selected organization.
A project API key cannot replace an Admin key.

```sh
openai costs report --from 2026-10-01 --to 2026-10-08 --timezone UTC --group-by project
openai costs report --from 2026-10-01 --to 2026-10-08 --project-id proj_example
openai --format json costs report --from 2026-10-01 --to 2026-10-08
openai costs report --from 2026-10-01 --to 2026-10-08 --export csv > costs.csv
```

The start date is inclusive. The end date is exclusive.
Both dates must use `YYYY-MM-DD`, on or after 1970-01-01.
The end must follow the start. Converted timestamps must not precede the Unix epoch.
`--timezone` defaults to `UTC` and accepts IANA names such as `America/Los_Angeles`.
The command converts each local midnight separately into Unix seconds.
A daylight-saving day can contain 23 or 25 hours.
Dates with nonexistent or ambiguous local midnights fail; use another boundary or UTC.
`Local`, relative dates, and automatic date guessing are unsupported.

The API accepts timestamps and daily buckets, without a timezone parameter.
This command totals the API results without prorating or changing bucket boundaries.
The API documentation does not promise timezone-aligned buckets.
Client tests verify submitted timestamps, not live billing reconciliation.

`--group-by project` is the default and only report grouping.
Repeat `--project-id` to filter several exact project IDs.
These IDs are literal strings, not `@file` references.
The root `--project` still sets the request header; it does not filter report rows.

## Output

Text output uses complete project IDs and amounts without color or a pager.
Long rows can wrap in narrow terminals.
Missing or null project IDs appear as `(unattributed)`.
Empty project IDs remain separate and appear as `(empty project ID)`.
Empty reports say `No cost records.` They do not invent a currency or a zero total.

JSON contains one document with these stable fields:

```json
{"from":"2026-10-01","to":"2026-10-08","timezone":"UTC","start_time":1790812800,"end_time":1791417600,"group_by":"project","rows":[{"project_id":"proj_example","currency":"usd","amount":"12.34"}]}
```

`rows` always contains an array, including `[]` for empty reports.
`project_id` is a string or `null`.
`amount` is an exact decimal string. It never passes through binary floating-point arithmetic.
Trailing decimal zeros are removed. Amounts are not rounded to two decimal places.
Negative amounts remain negative.
Very large exponents use exact scientific notation, such as `1e1000000000`, without expanding their zeros.
Each project and currency pair has a separate row.
Currency identifiers retain the API's spelling; the command does not convert currencies.
Rows sort by project ID and currency, with unattributed rows first.
New unrelated API fields do not change the report schema.
The generated `openai admin organization usage costs` command still exposes the original API response and filters.

CSV uses `project_id,currency,amount` headers and writes to stdout.
Use `--export csv` without `--format`; global `--format csv` remains unsupported.
CSV quotes commas, quotes, and newlines.
Formula-sensitive text cells receive an apostrophe prefix, including formulas after leading whitespace.
Text containing tabs or line breaks also receives that prefix.
Direct terminal output escapes display controls in text cells.
Redirected CSV preserves those characters after formula protection.
Numeric amounts remain exact decimal text.
Spreadsheet applications can round imported numbers. Use an exact decimal parser when consuming amounts.
CSV represents both null and empty project IDs as empty cells; JSON preserves their distinction.
Use JSON when exact textual identifiers must remain unchanged.

The report supports `--format auto`, `text`, and `json`.
It rejects `--transform`, `--raw-output`, and other output formats.
It ignores stdin. Dates and filters come only from flags.
Existing request headers, endpoint overrides, organization context, TLS, and Admin authentication remain available.

## Completion and failures

The command requests up to 180 daily buckets per page and follows every continuation cursor.
It continues across empty pages when the API reports more pages.
Missing or repeated cursors fail instead of silently returning an incomplete total.
Missing amounts, values, or currencies also fail instead of becoming zero.
Missing or unknown result types fail; unrelated new fields are ignored.
Repeated report fields fail, including repeated `data`, `amount`, or `value` keys.

Each project/currency total permits at most 1,048,576 extra digits beyond its longest original API coefficient.
The report tracks original coefficient lengths across every page.
Previously accumulated digits cannot enlarge this arithmetic budget.
The report checks the budget before allocating alignment digits.
Source coefficient lengths and API response sizes have no new limit.
Output switches to exact scientific notation when plain output would insert more than 1,048,576 zeros.
For example, `1e1000000000` remains compact, and two equal opposite amounts cancel exactly.
Adding `1` to that amount exceeds the report's arithmetic budget and fails without output.
The generated Costs command preserves raw API access for records that exceed this report-specific budget.
This limits each total's precision expansion. Overall memory still depends on response sizes and the number of distinct totals.

When this budget is exceeded, the error prints a runnable command to inspect raw Costs options.
It also prints the report's converted `--start-time` and `--end-time` values.
Use those values with `--group-by project_id --format raw` to inspect the original records.
Reapply the same project filters and request settings, including the endpoint, organization, credentials, headers, and TLS options.
The error does not copy those settings into a command because they can contain secrets.
Each generated Costs request returns one page.
Pass its `next_page` value as `--page` while `has_more` is true.
Do not treat one raw page as a complete report.

No report reaches stdout until every page succeeds.
Any request, parsing, or pagination failure exits nonzero without a partial total.
Ctrl+C cancels requests and aggregation, then exits with status 130.
Output failures also exit nonzero; a redirected file can contain incomplete output.
Shell redirection can create or truncate its destination before the command runs.

This feature reports money returned by the Costs API.
It does not report token usage, estimate prices, change billing, resolve project names, or reconcile invoices.
Other usage and billing workflows remain separate.
No live organization account was used for the synthetic client tests.

API contract: [Costs reference](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/usage/methods/costs).
