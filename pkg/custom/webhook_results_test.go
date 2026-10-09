package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const webhookResultFixture = `{"object":"webhook_endpoint.test","webhook_endpoint_id":"whe_example","event_type":"response.completed","status_code":500,"success":true}`

func webhookResultOptions(operation string) ShowJSONOpts {
	ctx := context.WithValue(context.Background(), webhookCommandKey{}, webhookCommandContext{
		operation:  operation,
		invocation: fileInvocation{display: "openai", executable: "openai"},
	})
	return ShowJSONOpts{Context: ctx, Operation: "(resource) webhooks > (method) " + operation, OutputKind: OutputResponse}
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
			handled, err := showWebhookResult(gjson.Parse(body), opts)
			if !handled || err != nil || !strings.HasPrefix(stdout.String(), "Test request completed.\n") ||
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
			if handled, err := showWebhookResult(gjson.Parse(webhookResultFixture), opts); handled || err != nil || out.Len() != 0 {
				t.Fatalf("explicit mode intercepted: %t %v %q", handled, err, out.String())
			}
		})
	}
	var stdout, stderr bytes.Buffer
	opts := webhookResultOptions("test")
	opts.Stdout, opts.Stderr = &stdout, &stderr
	opts.Context = context.WithValue(opts.Context, outputPolicyKey{}, outputPolicy{quiet: true})
	if handled, err := showWebhookResult(gjson.Parse(webhookResultFixture), opts); !handled || err != nil || stdout.Len() == 0 || stderr.Len() != 0 {
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
		if handled, err := showWebhookResult(gjson.Parse(body), opts); !handled || err != nil {
			t.Fatalf("fallback: %t %v", handled, err)
		}
		if !strings.Contains(stdout.String(), "Success: true") || strings.Contains(stdout.String(), "Delivery failed") ||
			!strings.Contains(stderr.String(), "No delivery outcome was inferred") {
			t.Fatalf("unexpected response lost its boundary: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		if strings.Contains(body, "9007199254740993") && !strings.Contains(stdout.String(), "9007199254740993") {
			t.Fatal("precise unknown field was lost")
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
	if _, err := showWebhookResult(gjson.Parse(webhookResultFixture), opts); !errors.Is(err, failure) || stderr.Len() != 0 {
		t.Fatalf("advice followed a failed result: %v %q", err, stderr.String())
	}
	var stdout bytes.Buffer
	opts = webhookResultOptions("create")
	opts.Stdout, opts.Stderr = &stdout, webhookFailedWriter{failure}
	body := `{"object":"webhook_endpoint","id":"whe_example","event_types":["response.completed"],"signing_secret":"whsec_synthetic_only","future":"retained"}`
	if _, err := showWebhookResult(gjson.Parse(body), opts); !errors.Is(err, failure) ||
		!strings.Contains(stdout.String(), "whsec_synthetic_only") || !strings.Contains(stdout.String(), "Future: retained") {
		t.Fatalf("created result was lost after diagnostic failure: %q %v", stdout.String(), err)
	}
	opts.Stdout, opts.Stderr = io.Discard, &stderr
	if _, err := showWebhookResult(gjson.Parse(body), opts); err != nil || strings.Contains(stderr.String(), "whsec_synthetic_only") {
		t.Fatalf("secret entered guidance: %q %v", stderr.String(), err)
	}
	stderr.Reset()
	opts.Stdout = webhookFailedWriter{failure}
	_, err := showWebhookResult(gjson.Parse(body), opts)
	var workflowFailure *webhookWorkflowError
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
				opts.Operation = "(resource) webhooks.event_types > (method) list"
			}
			opts.Stdout, opts.Stderr = &stdout, &stderr
			if handled, err := showWebhookResult(gjson.Parse(tc.body), opts); !handled || err != nil ||
				!strings.Contains(stderr.String(), tc.advice) || strings.Contains(stderr.String(), "Choose events interactively") {
				t.Fatalf("misleading next step: handled=%t error=%v stdout=%q stderr=%q", handled, err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWebhookRecoveryErrorsPreserveCausesAndPrivacy(t *testing.T) {
	private := "private-home-secret-token"
	for _, cause := range []error{
		&os.PathError{Op: "write", Path: "/" + private, Err: os.ErrPermission},
		&os.LinkError{Op: "link", Old: "/" + private, New: "/other-private-path", Err: os.ErrPermission},
		errors.Join(&os.PathError{Op: "write", Path: "/" + private, Err: os.ErrPermission}, errors.New(private)),
		context.DeadlineExceeded,
	} {
		failure := webhookCreateOutcomeError(cause)
		for _, wrapped := range []error{failure, fmt.Errorf("%s: %w", private, failure), errors.Join(errors.New(private), failure)} {
			message := localErrorMessage(&cli.Command{}, wrapped)
			if !errors.Is(wrapped, cause) || strings.Contains(message, private) ||
				!strings.Contains(message, "original authentication, project, organization, and API settings") ||
				!strings.Contains(message, "before repeating create") {
				t.Fatalf("unsafe or incomplete recovery: %q", message)
			}
			var output bytes.Buffer
			if err := ShowCommandError(readableErrorTestCommand(t), wrapped, &output); err != nil ||
				strings.Contains(output.String(), private) || !strings.Contains(output.String(), message) {
				t.Fatalf("presenter lost safe recovery: error=%v output=%q", err, output.String())
			}
		}
	}
	canceled := errors.Join(context.Canceled, &webhookWorkflowError{message: "Creation may exist.", cause: os.ErrPermission})
	if got := localErrorMessage(&cli.Command{}, canceled); got != "Request canceled." {
		t.Fatalf("cancellation lost precedence: %q", got)
	}
}

func TestWebhookSubmittedCancellationUsesExistingErrorPresenter(t *testing.T) {
	const private = "synthetic-private-path-token"
	cause := errors.Join(context.Canceled, &os.PathError{Op: "write", Path: private, Err: os.ErrPermission})
	failure := &webhookCreateCanceledError{cause: cause}
	for _, format := range []string{"text", "json", "jsonl"} {
		for _, wrapped := range []error{failure, fmt.Errorf("%s: %w", private, failure), errors.Join(errors.New(private), failure)} {
			var out bytes.Buffer
			if err := ShowCommandError(readableErrorTestCommand(t, "--format-error", format), wrapped, &out); err != nil {
				t.Fatal(err)
			}
			message := strings.TrimSpace(out.String())
			if format != "text" {
				var value struct{ Message string }
				if err := json.Unmarshal(out.Bytes(), &value); err != nil {
					t.Fatalf("structured cancellation was polluted: %v, %q", err, out.String())
				}
				message = value.Message
			}
			if message != failure.Error() || strings.Contains(out.String(), private) || !errors.Is(wrapped, context.Canceled) {
				t.Fatalf("submitted cancellation lost safe recovery: %q", out.String())
			}
		}
	}
}
