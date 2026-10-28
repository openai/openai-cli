package webhooks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

const webhookResultFixture = `{"object":"webhook_endpoint.test","webhook_endpoint_id":"whe_example","event_type":"response.completed","status_code":500,"success":true}`

func webhookResultOptions(operation string) ResultOptions {
	request := new(atomic.Pointer[webhookTestRequest])
	request.Store(&webhookTestRequest{endpoint: "whe_example", event: "response.completed"})
	ctx := context.WithValue(context.Background(), webhookCommandKey{}, webhookCommandContext{
		operation:  operation,
		invocation: webhookInvocation{display: "openai", executable: "openai"},
		request:    request,
	})
	return ResultOptions{ReadableOptions: output.ReadableOptions{Context: ctx,
		Route:              transformers.Route{Operation: "(resource) webhooks > (method) " + operation, OutputKind: transformers.OutputResponse},
		DiagnosticsAllowed: true}, WriteHint: func(ctx context.Context, out io.Writer, message string) error {
		if err := readable.WriteText(output.Writer{Context: ctx, Sink: out}, message); err != nil {
			return &output.DiagnosticError{Err: err}
		}
		return nil
	}}
}

func TestWebhookResultGuidancePreservesOutputBoundary(t *testing.T) {
	for _, status := range []struct{ code, advice string }{
		{"200", "processed the sample event"}, {"202", "not completed application work"},
		{"300", "final HTTPS destination"}, {"400", "JSON validation"}, {"401", "signature verification"},
		{"403", "signature verification"}, {"404", "exact path is deployed"}, {"405", "HTTP POST"},
		{"408", "timeouts"}, {"413", "payload's size"}, {"415", "application/json"},
		{"422", "JSON validation"}, {"429", "rate limits"}, {"500", "receiver and proxy logs"}, {"504", "timeouts"},
	} {
		t.Run(status.code, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			opts := webhookResultOptions("test")
			opts.Stdout, opts.Stderr = &stdout, &stderr
			body := strings.Replace(webhookResultFixture, "500", status.code, 1)
			handled, err := ShowResult(gjson.Parse(body), opts)
			if !handled || err != nil || !strings.Contains(stdout.String(), "\n\nTest request completed.\n") ||
				!strings.Contains(stderr.String(), status.advice) || strings.Contains(stdout.String(), "Next:") {
				t.Fatalf("result: handled=%t error=%v stdout=%q stderr=%q", handled, err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWebhookResultMachineModesAndQuiet(t *testing.T) {
	for _, mode := range []string{"json", "jsonl", "raw", "yaml", "pretty", "explore", "extract", "raw-output"} {
		t.Run(mode, func(t *testing.T) {
			var out bytes.Buffer
			opts := webhookResultOptions("test")
			opts.Stdout, opts.Stderr, opts.Format = &out, &out, mode
			if mode == "extract" {
				opts.Format, opts.Transform = "text", "status_code"
			} else if mode == "raw-output" {
				opts.Format, opts.RawOutput = "text", true
			}
			if handled, err := ShowResult(gjson.Parse(webhookResultFixture), opts); handled || err != nil || out.Len() != 0 {
				t.Fatalf("explicit mode intercepted: %t %v %q", handled, err, out.String())
			}
		})
	}
	var stdout, stderr bytes.Buffer
	opts := webhookResultOptions("test")
	opts.Stdout, opts.Stderr = &stdout, &stderr
	opts.DiagnosticsAllowed = false
	if handled, err := ShowResult(gjson.Parse(webhookResultFixture), opts); !handled || err != nil || stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("quiet changed result: %t %v %q %q", handled, err, stdout.String(), stderr.String())
	}
}

func TestWebhookUnexpectedResultPreservesFields(t *testing.T) {
	for _, body := range []string{
		strings.Replace(webhookResultFixture, "500", "0", 1),
		strings.TrimSuffix(webhookResultFixture, "}") + `,"future":{"count":9007199254740993}}`,
	} {
		var stdout, stderr bytes.Buffer
		opts := webhookResultOptions("test")
		opts.Stdout, opts.Stderr = &stdout, &stderr
		if handled, err := ShowResult(gjson.Parse(body), opts); !handled || err != nil {
			t.Fatalf("fallback: %t %v", handled, err)
		}
		if !strings.Contains(stdout.String(), "Success: true") || strings.Contains(stdout.String(), "Delivery failed") ||
			!strings.Contains(stderr.String(), "No delivery outcome was inferred") {
			t.Fatalf("unexpected response lost its boundary: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		if strings.Contains(body, "9007199254740993") && !strings.Contains(stdout.String(), "9007199254740993") {
			t.Fatal("precise unknown field was lost")
		}
		if !strings.HasPrefix(stdout.String(), "Project: determined by API key (ID unavailable)\nEndpoint: whe_example\nEvent: response.completed\n\n") {
			t.Fatalf("fallback lost submitted request context: %q", stdout.String())
		}
	}
}

type webhookFailedWriter struct{ err error }

func (w webhookFailedWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWebhookResultWriteFailuresAndSecret(t *testing.T) {
	failure := errors.New("synthetic output failure")
	var stderr bytes.Buffer
	opts := webhookResultOptions("test")
	opts.Stdout, opts.Stderr = webhookFailedWriter{failure}, &stderr
	if _, err := ShowResult(gjson.Parse(webhookResultFixture), opts); !errors.Is(err, failure) || stderr.Len() != 0 {
		t.Fatalf("advice followed a failed result: %v %q", err, stderr.String())
	}
	var stdout bytes.Buffer
	opts = webhookResultOptions("create")
	opts.Stdout, opts.Stderr = &stdout, webhookFailedWriter{failure}
	body := `{"object":"webhook_endpoint","id":"whe_example","event_types":["response.completed"],"signing_secret":"whsec_synthetic_only","future":"retained"}`
	if _, err := ShowResult(gjson.Parse(body), opts); !errors.Is(err, failure) ||
		!strings.Contains(stdout.String(), "whsec_synthetic_only") || !strings.Contains(stdout.String(), "Future: retained") {
		t.Fatalf("created result was lost after diagnostic failure: %q %v", stdout.String(), err)
	}
	opts.Stdout, opts.Stderr = io.Discard, &stderr
	if _, err := ShowResult(gjson.Parse(body), opts); err != nil || strings.Contains(stderr.String(), "whsec_synthetic_only") {
		t.Fatalf("secret entered guidance: %q %v", stderr.String(), err)
	}
	stderr.Reset()
	opts.Stdout = webhookFailedWriter{failure}
	_, err := ShowResult(gjson.Parse(body), opts)
	var workflowFailure *WorkflowError
	if !errors.Is(err, failure) || !errors.As(err, &workflowFailure) ||
		!strings.Contains(workflowFailure.Error(), "before repeating create") ||
		strings.Contains(workflowFailure.Error(), "whsec_synthetic_only") || stderr.Len() != 0 {
		t.Fatalf("create output failure lost safe recovery: %v, stderr=%q", err, stderr.String())
	}
}

func TestWebhookCommandValueIsOnlyAnOptionalHintFilter(t *testing.T) {
	for _, value := range []string{"@private", `\@private`, "a\nforged", "$(touch synthetic)", "a;b", "", strings.Repeat("a", 257)} {
		if webhookCommandValue(value) {
			t.Fatalf("unsafe hint value accepted: %q", value)
		}
	}
	for _, value := range []string{"whe_example", "response.completed", "future-event:ready", "--leading-dash"} {
		if !webhookCommandValue(value) {
			t.Fatalf("safe hint value refused: %q", value)
		}
	}
}

func TestWebhookCreatedAdviceDoesNotSuggestRetiredEvents(t *testing.T) {
	for _, events := range []string{`["fine_tuning.job.succeeded"]`, `["fine_tuning.job.succeeded","response.completed"]`} {
		var stdout, stderr bytes.Buffer
		opts := webhookResultOptions("create")
		opts.Stdout, opts.Stderr = &stdout, &stderr
		body := `{"object":"webhook_endpoint","id":"whe_example","event_types":` + events + `}`
		handled, err := ShowResult(gjson.Parse(body), opts)
		if !handled || err != nil || !strings.Contains(stdout.String(), "fine_tuning.job.succeeded") ||
			strings.Contains(stderr.String(), "fine_tuning") {
			t.Fatalf("subscription data or advice changed incorrectly: handled=%t err=%v stdout=%q stderr=%q", handled, err, stdout.String(), stderr.String())
		}
		if strings.Contains(events, "response.completed") != strings.Contains(stderr.String(), "--event-type=response.completed") {
			t.Fatalf("supported follow-up was lost: %q", stderr.String())
		}
	}
}

func TestWebhookRetiredEventHasNoRetryCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := webhookResultOptions("test")
	state, _ := opts.Context.Value(webhookCommandKey{}).(webhookCommandContext)
	state.request.Store(&webhookTestRequest{endpoint: "whe_example", event: "fine_tuning.job.succeeded"})
	opts.Stdout, opts.Stderr = &stdout, &stderr
	handled, err := ShowResult(gjson.Parse(webhookResultFixture), opts)
	if !handled || err != nil || strings.Contains(stderr.String(), "--event-type=fine_tuning") {
		t.Fatalf("retired retry command returned: %t %v %q", handled, err, stderr.String())
	}
}

func TestWebhookEventCatalogMachineModesRemainUntouched(t *testing.T) {
	for _, mode := range []string{"json", "jsonl", "yaml", "pretty", "raw", "extract", "raw-output"} {
		var out bytes.Buffer
		opts := webhookResultOptions("event-types")
		opts.Stdout, opts.Stderr, opts.Format = &out, &out, mode
		if mode == "extract" {
			opts.Format, opts.Transform = "text", "data"
		} else if mode == "raw-output" {
			opts.Format, opts.RawOutput = "text", true
		}
		opts.Route.Operation = "(resource) webhooks.event_types > (method) list"
		value := gjson.Parse(`{"object":"list","data":["fine_tuning.job.succeeded","response.completed"]}`)
		if handled, err := ShowResult(value, opts); handled || err != nil || out.Len() != 0 {
			t.Fatalf("machine catalog was intercepted: mode=%s handled=%t error=%v output=%q", mode, handled, err, out.String())
		}
	}
}

func TestWebhookReadableEventCatalogExcludesRetiredEvents(t *testing.T) {
	for _, events := range []string{`["fine_tuning.job.succeeded","response.completed"]`, `["fine_tuning.future.event"]`} {
		var stdout, stderr bytes.Buffer
		opts := webhookResultOptions("event-types")
		opts.Stdout, opts.Stderr = &stdout, &stderr
		opts.Route.Operation = "(resource) webhooks.event_types > (method) list"
		value := gjson.Parse(`{"object":"list","data":` + events + `}`)
		handled, err := ShowResult(value, opts)
		if !handled || err != nil || strings.Contains(stdout.String(), "fine_tuning") {
			t.Fatalf("readable catalog retained retired choices: %t %v %q", handled, err, stdout.String())
		}
		if strings.Contains(events, "response.completed") {
			if !strings.Contains(stdout.String(), "response.completed") {
				t.Fatalf("supported event disappeared: %q", stdout.String())
			}
		} else if !strings.Contains(stdout.String(), "The available webhook events are not supported by the CLI.") ||
			!strings.Contains(stderr.String(), "Use the Platform API directly for these events.") ||
			strings.Contains(stderr.String(), "Choose events interactively") || strings.Contains(stderr.String(), "access") {
			t.Fatalf("retired-only catalog suggests unavailable choices: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

func TestWebhookResultUnexpectedStringAndEmptyCatalog(t *testing.T) {
	for _, tc := range []struct{ operation, body, advice string }{
		{"test", `"unfamiliar result"`, "No delivery outcome was inferred"},
		{"event-types", `{"object":"list","data":[]}`, "check your project and access settings"},
		{"event-types", `{"object":"list","data":[],"future":true}`, "unexpected event catalog"},
	} {
		t.Run(tc.operation+tc.body, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			opts := webhookResultOptions(tc.operation)
			if tc.operation == "event-types" {
				opts.Route.Operation = "(resource) webhooks.event_types > (method) list"
			}
			opts.Stdout, opts.Stderr = &stdout, &stderr
			if handled, err := ShowResult(gjson.Parse(tc.body), opts); !handled || err != nil ||
				!strings.Contains(stderr.String(), tc.advice) || strings.Contains(stderr.String(), "Choose events interactively") {
				t.Fatalf("misleading next step: handled=%t error=%v stdout=%q stderr=%q", handled, err, stdout.String(), stderr.String())
			}
		})
	}
}
