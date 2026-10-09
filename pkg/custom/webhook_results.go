package custom

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

const webhookReceiverGuide = "https://developers.openai.com/api/docs/guides/webhooks"

// Keep complete result output separate from optional, quiet-aware next steps.
func showWebhookResult(value gjson.Result, opts ShowJSONOpts) (bool, error) {
	opts.setDefaults()
	command, ok := opts.Context.Value(webhookCommandKey{}).(webhookCommandContext)
	if !ok || opts.OutputKind != OutputResponse || opts.Transform != "" || opts.RawOutput || resolvedOutputFormat(opts) != "text" {
		return false, nil
	}
	if err := opts.Context.Err(); err != nil {
		return true, err
	}
	var advice string
	projected := value
	switch {
	case command.operation == "test" && opts.Operation == "(resource) webhooks > (method) test":
		var err error
		projected, err = transformers.ProjectWebhookTestResult(opts.Context, value)
		if err != nil {
			return true, err
		}
		if value.IsObject() && projected.Type == gjson.String {
			advice = webhookTestAdvice(int(value.Get("status_code").Int()))
			if value.Get("status_code").Int() < 200 || value.Get("status_code").Int() >= 300 {
				id, event := value.Get("webhook_endpoint_id").Str, value.Get("event_type").Str
				if webhookCommandValue(id) {
					if inspect := webhookFollowupCommand(opts.Context, "webhooks", "retrieve", "--webhook-endpoint-id="+id); inspect != "" {
						advice += "\nInspect the receiver URL: " + inspect
					}
				}
				if webhookCommandValue(id) && webhookCommandValue(event) {
					if retry := webhookFollowupCommand(opts.Context, "webhooks", "test", "--webhook-endpoint-id="+id, "--event-type="+event); retry != "" {
						advice += "\nAfter fixing the receiver, retry: " + retry
					} else {
						advice += "\nAfter fixing the receiver, repeat your original command to keep its authentication and project settings."
					}
				}
			}
		} else {
			advice = "The API returned an unexpected test result. No delivery outcome was inferred.\n" +
				"Next: inspect the returned fields and your receiver logs before sending another test."
		}
	case command.operation == "create" && opts.Operation == "(resource) webhooks > (method) create":
		advice = webhookCreatedAdvice(opts.Context, value)
	case command.operation == "event-types" && opts.Operation == "(resource) webhooks.event_types > (method) list":
		var err error
		projected, err = transformers.ProjectWebhookEventTypes(opts.Context, value)
		if err != nil {
			return true, err
		}
		switch {
		case projected.Raw == value.Raw:
			advice = "The API returned an unexpected event catalog.\nNext: inspect the returned fields and check your project access before creating an endpoint."
		case projected.Type == gjson.String:
			advice = "Next: check your project and access settings. Load event types again before creating an endpoint."
		default:
			advice = "Next: select the events your receiver handles. Repeat --event-type to subscribe to multiple events."
			if create := webhookFollowupCommand(opts.Context, "webhooks", "create"); create != "" {
				advice += "\nChoose events interactively: " + create
			}
		}
	default:
		return false, nil
	}
	if err := readable.Write(outputWriter{ctx: opts.Context, out: opts.Stdout}, projected); err != nil {
		if command.operation == "create" {
			return true, webhookCreateOutcomeError(err)
		}
		return true, err
	}
	return true, writeOutputHint(opts, advice)
}

func webhookTestAdvice(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "Next: confirm your receiver verified the signature and processed the sample event.\nA 2xx response confirms receipt, not completed application work."
	case status >= 300 && status < 400:
		return "Next: update the endpoint URL to its final HTTPS destination. Webhook tests do not follow redirects."
	case status == 400 || status == 422:
		return "Next: check your receiver logs and JSON validation for this sample event."
	case status == 401 || status == 403:
		return "Next: check receiver access rules and webhook signature verification.\nUse the endpoint's signing secret to verify the original request bytes.\nReceiver guide: " + webhookReceiverGuide
	case status == 404:
		return "Next: check the endpoint URL and confirm that its exact path is deployed on your receiver."
	case status == 405:
		return "Next: check that the receiver route accepts HTTP POST requests."
	case status == 408 || status == 504:
		return "Next: check receiver and proxy timeouts. Acknowledge promptly, then process work asynchronously."
	case status == 413:
		return "Next: check whether your receiver or proxy rejects the sample payload's size."
	case status == 415:
		return "Next: check that your receiver accepts application/json requests."
	case status == 429:
		return "Next: check receiver rate limits and capacity before sending another test."
	case status >= 500:
		return "Next: check your receiver and proxy logs for this test. Fix the server error before retrying."
	default:
		return "Next: check your receiver logs and request handling before sending another test."
	}
}

func webhookCreatedAdvice(ctx context.Context, value gjson.Result) string {
	if !value.IsObject() || !gjson.Valid(value.Raw) || value.Get("object").Str != "webhook_endpoint" || !webhookCommandValue(value.Get("id").Str) {
		return "The API returned unexpected endpoint data. Keep this response.\nNext: list your endpoints before repeating create, to avoid creating a duplicate."
	}
	advice := "Next: save the signing secret from this response securely; it is returned only on creation or rotation.\n" +
		"Configure your receiver to verify signatures with that secret before processing events.\nReceiver guide: " + webhookReceiverGuide
	if secret := value.Get("signing_secret"); secret.Type != gjson.String || secret.Str == "" {
		advice = "No signing secret was returned. Keep this response and check your saved secret before testing.\nReceiver guide: " + webhookReceiverGuide
	}
	for _, event := range value.Get("event_types").Array() {
		if event.Type == gjson.String && webhookCommandValue(event.Str) {
			if command := webhookFollowupCommand(ctx, "webhooks", "test", "--webhook-endpoint-id="+value.Get("id").Str, "--event-type="+event.Str); command != "" {
				advice += "\nWhen your receiver is ready, send a sample: " + command
			}
			break
		}
	}
	return advice
}

// Restrict optional command suggestions, never the API response or request.
// Printable event names and IDs need no @ expansion or shell interpretation.
func webhookCommandValue(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r)) {
			return false
		}
	}
	return true
}

func webhookFollowupCommand(ctx context.Context, args ...string) string {
	command, ok := ctx.Value(webhookCommandKey{}).(webhookCommandContext)
	if !ok || command.invocation.omitHint || ctx.Err() != nil {
		return ""
	}
	invocation := command.invocation
	args = append(append([]string{}, invocation.requestArgs...), args...)
	shell := fileReceiptShell(ctx, invocation)
	if shell == "" {
		// Plain tokens use the same syntax in the supported shells. Quoted paths
		// and overrides need a verified shell; never infer it from SHELL.
		if plain, _ := imagePickerQuoteProperties(invocation.display); !plain || invocation.goRun {
			return ""
		}
		for _, arg := range args {
			if plain, _ := imagePickerQuoteProperties(strings.ReplaceAll(arg, "=", "")); !plain {
				return ""
			}
		}
		return strings.Join(append([]string{invocation.display}, args...), " ")
	}
	prefix := fileReceiptInvocation(invocation, shell)
	if prefix == "" {
		return ""
	}
	return prefix + strings.TrimPrefix(formatImagePickerCommand(args, shell), "openai")
}
