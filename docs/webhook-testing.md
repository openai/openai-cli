# Webhook receiver tests

Send a sample event to an existing project webhook endpoint:

```sh
openai webhooks test --webhook-endpoint-id wh_demo --event-type response.completed
```

Replace `wh_demo` with your endpoint ID. The command uses your configured project credentials.
This command sends an actual test delivery when connected to the OpenAI API.

Readable output separates test completion from the receiver's HTTP response:

```text
Test request completed.
Delivery failed: endpoint returned HTTP 500.
Webhook endpoint ID: "wh_demo"
Event type: "response.completed"
```

Check your receiver logs when delivery fails. Fix the receiver error before running the test again.

A receiver response from HTTP 200 through 299 produces `Delivery accepted`.
Acceptance confirms the receiver's HTTP response. It does not confirm that your application finished processing the event.
Other HTTP responses produce `Delivery failed`, including redirects.

The API's `success: true` field means the test request completed.
It does not mean the receiver accepted the delivery.
A completed test keeps exit status 0, including a receiver HTTP 500 response.
API errors and test execution failures keep the existing nonzero exit status and error output.
The API does not define a separate `success: false` result.
See the [test response contract](https://developers.openai.com/api/reference/cli/resources/webhooks/methods/test).

The summary shows only endpoint and event context returned by the API.
Quotes preserve single-line context when values contain control characters.
Unknown fields, missing fields, duplicate keys, and malformed values retain the complete generic readable output.

Use the existing explicit output options for automation:

```sh
# Preserve the API response fields.
openai webhooks test --webhook-endpoint-id wh_demo --event-type response.completed --format json

# Extract the receiver's HTTP status.
openai webhooks test --webhook-endpoint-id wh_demo --event-type response.completed --transform status_code --raw-output
```

Explicit JSON, JSONL, raw, YAML, and extraction keep their existing behavior.
Default, `--format auto`, and `--format text` output use the readable summary.
No new command, flag, receiver listener, or replay service is added.
