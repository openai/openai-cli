# Create and test webhooks

A webhook sends an HTTP POST request to your server when a subscribed event occurs.
For example, `response.completed` tells your server that a background response finished.
Your server needs a public HTTPS URL that accepts these requests.
The CLI configures endpoints and sends samples through the API; it does not host your receiver.

You can also use the [project webhook settings](https://platform.openai.com/settings/project/webhooks) in the dashboard.
The [receiver guide](https://developers.openai.com/api/docs/guides/webhooks) explains signature verification and event handling.

## Create an endpoint

Start the guided flow in a terminal:

```sh
openai webhooks create
```

1. Enter a name. Leave it blank to use the displayed `Webhook` default.
2. Enter your receiver's HTTPS URL.
3. Select the events your receiver handles.
4. Review the settings.
5. Choose Yes to create the endpoint.

The CLI loads available events from your authenticated project.
Type to search the grouped list. Use arrow keys to move and Space to select events.
Selections remain when you change the search. Press Enter to review them.
Use Shift+Tab to go back. Press Escape or Ctrl+C to cancel.
The final confirmation defaults to No.

Creation returns a signing secret. Save it securely before closing the terminal.
Configure your receiver to verify webhook signatures with that secret before processing events.
The API returns the secret only during creation or rotation.

The guided flow needs terminal input and output.
Request flags, piped input, explicit output options, quiet mode, and CI retain the existing command behavior.
Use explicit flags for scripts:

```sh
openai webhooks create \
  --name "Response notifications" \
  --url https://example.com/webhook \
  --event-type response.completed \
  --event-type response.failed \
  --format json
```

Replace the example URL with your receiver URL. The API requires a name in explicit requests.
Repeat `--event-type` for each subscription. Existing JSON/YAML input remains supported.
See the [create reference](https://developers.openai.com/api/reference/cli/resources/webhooks/methods/create).

## Discover available operations

```sh
# Show the available commands.
openai webhooks --help

# Show every create option.
openai webhooks create --help

# List event names available to your project.
openai webhooks event-types list

# Find your endpoint ID.
openai webhooks list

# Inspect the configured URL and subscriptions.
openai webhooks retrieve --webhook-endpoint-id whe_example
```

The readable event catalog groups current API names by category.
New event names remain available without a CLI update.
Use `--format json` for the original catalog fields.
The existing `webhooks:event-types list` spelling remains supported.

`update` changes settings. Supplying `--event-type` replaces the complete event set.
`rotate-secret` returns a new signing secret.
Its `--keep-old-secret-active-for-24-hours` flag permits a transition; otherwise, the previous secret becomes invalid immediately.
`delete` removes an endpoint. Inspect its ID before deleting it.

## Test your receiver

After configuring signature verification, send a sample event:

```sh
openai webhooks test --webhook-endpoint-id whe_example --event-type response.completed
```

Replace `whe_example` with your endpoint ID. This command sends an actual sample when connected to the OpenAI API.
Readable output separates test completion from the receiver's HTTP response:

```text
Test request completed.
Delivery failed: endpoint returned HTTP 500.
Webhook endpoint ID: "whe_example"
Event type: "response.completed"
```

The CLI also writes recovery guidance to stderr.
For HTTP 500, it recommends checking receiver and proxy logs before retrying.
When safe, it prints commands to inspect the endpoint and repeat the test.
These commands preserve the executable and explicit project, organization, and base URL settings.
The CLI omits copied commands when credentials or private headers would be needed.
Use `--quiet` to suppress optional next steps.

| Receiver response | Next action |
| --- | --- |
| 2xx | Confirm signature verification and application processing. |
| 3xx | Set the final HTTPS URL. Webhook tests do not follow redirects. |
| 400 or 422 | Check receiver logs and JSON validation. |
| 401 or 403 | Check access rules and signature verification against the original request bytes. |
| 404 | Check the configured path and receiver deployment. |
| 405 | Allow HTTP POST on that route. |
| 408 or 504 | Check timeouts. Acknowledge promptly and process work asynchronously. |
| 413 or 415 | Check payload size handling or JSON content handling. |
| 429 | Check receiver rate limits and capacity. |
| Other 5xx | Fix the receiver or proxy error before retrying. |

A 2xx response confirms receipt. It does not establish that your application finished processing the event.
The API's `success: true` means the test request completed.
A completed test keeps exit status 0, including a receiver HTTP 500 response.
API errors keep their existing nonzero status and error output.
See the [test reference](https://developers.openai.com/api/reference/cli/resources/webhooks/methods/test).

Unknown fields, missing fields, duplicate keys, and malformed results retain the complete generic readable output.
The CLI does not infer a delivery outcome from an unexpected result.

## Preserve machine output

```sh
# Preserve the API response fields.
openai webhooks test --webhook-endpoint-id whe_example --event-type response.completed --format json

# Extract the receiver's HTTP status.
openai webhooks test --webhook-endpoint-id whe_example --event-type response.completed --transform status_code --raw-output
```

Explicit JSON, JSONL, raw, YAML, and extraction keep their existing behavior.
Default, `--format auto`, and `--format text` use readable results and optional next steps.
The CLI prints the complete create response before optional guidance, including the one-time signing secret.

If creation loses its response, list endpoints before repeating it. The API might have created the endpoint already.
Cancellation after submission also shows this uncertainty and tells you to inspect the original project before repeating create.
The guided flow adds no retry loop; the existing SDK retry policy still applies.
If you lose a signing secret, rotate it and update your receiver.
