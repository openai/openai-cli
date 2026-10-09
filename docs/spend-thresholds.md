# Spend thresholds

Spend-limit and spend-alert amounts use cents. Readable output shows integer USD amounts as dollars and cents.

```sh
# Read the organization threshold.
openai admin organization spend-limit retrieve

# Read a project threshold.
openai admin organization projects spend-limit retrieve --project-id proj_example

# List notification thresholds.
openai admin organization spend-alerts list
```

A response containing `threshold_amount: 10000`, `currency: "USD"`, and `interval: "month"` displays:

```text
Spend threshold: USD 100.00 per month
```

The CLI preserves reported enforcement fields. When enforcement is absent or null, it displays:

```text
Enforcement: not reported in this response
```

Spend alerts notify recipients. They do not enforce spending caps or stop API requests.
Each readable alert includes this distinction, including alerts returned across multiple pages.
Notification channels, recipients, identifiers, and unfamiliar fields remain visible.

## Preserved data and inputs

The projection applies to default readable output and `--format text`.
Organization and project responses share these rules.
Successful create/update responses use the same presentation as retrieve/list responses.
Delete confirmations remain unchanged.

```sh
# Preserve the original numeric cents and response fields.
openai admin organization spend-limit retrieve --format json

# Extract the original amount.
openai admin organization spend-limit retrieve --transform threshold_amount --raw-output

# Preserve existing pagination controls.
openai admin organization spend-alerts list --max-items -1
```

Explicit JSON, JSONL, YAML, raw, pretty, and explore formats retain their existing data behavior.
Extraction and `--raw-output` bypass the projection.
The CLI does not change errors, exit status, or pagination.

Unknown currencies retain the original amount in cents without assuming a currency conversion.
Unknown intervals remain literal values. Missing currency or interval receives a `not reported` label.
The CLI preserves exact large integers without floating-point conversion.
Unexpected decimal or exponent values retain their original numeric spelling in cents.
Responses with duplicate top-level keys retain their original readable fields instead of a summary.
This avoids assigning ambiguous currency, interval, or enforcement information to a threshold.

Existing update/create flags still accept cents. This change does not convert inputs or change validation.
Limit inputs require at least one cent in the API schema; alert inputs allow zero.
Response edge-case handling does not establish new accepted mutation inputs.

## Scope

The bundled API contract establishes USD, monthly intervals, and reported limit enforcement states.
The CLI does not infer an enforcement state from the threshold amount.
This presentation change adds no billing action, policy API, usage-tier discovery, or Costs API conversion.
Costs API amounts follow a separate contract.

See the [organization spend-limit reference](https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/spend_limit/methods/retrieve)
and [spend-alert guidance](https://developers.openai.com/api/docs/guides/terraform/rate-limits-and-spend).
