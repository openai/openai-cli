package webhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebhookRequestContextPreservesRequest(t *testing.T) {
	for _, body := range []string{`{"event_type":"response.completed"}`, `{"event_type":null}`, `{"event_type":42}`, `{"EVENT_TYPE":"batch.failed","event_type":null}`, `{"EVENT_TYPE":"batch.failed"}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			state := new(atomic.Pointer[webhookTestRequest])
			ctx := context.WithValue(context.Background(), webhookCommandKey{}, webhookCommandContext{operation: "test", request: state})
			request, err := http.NewRequestWithContext(ctx, "POST", "https://api.invalid/webhook_endpoints/proxy/v1/webhook_endpoints/whe_%40literal/test", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("OpenAI-Project", "proj_submitted")
			called := false
			_, err = CaptureTestRequest(request, func(sent *http.Request) (*http.Response, error) {
				called = true
				got, readErr := io.ReadAll(sent.Body)
				if readErr != nil || string(got) != body || sent != request {
					t.Fatalf("request changed: body=%q error=%v", got, readErr)
				}
				return nil, nil
			})
			value := state.Load()
			if err != nil || !called || value == nil || value.project != "proj_submitted" || value.endpoint != "whe_@literal" {
				t.Fatalf("capture=%+v called=%t error=%v", value, called, err)
			}
			wantEvent := ""
			if strings.Contains(body, "response.completed") {
				wantEvent = "response.completed"
			}
			if value.event != wantEvent {
				t.Fatalf("event=%q want=%q", value.event, wantEvent)
			}
		})
	}
}

func TestWebhookRequestContextDoesNotConsumeOtherRequests(t *testing.T) {
	for _, command := range []webhookCommandContext{{operation: "create"}, {operation: "test"}} {
		ctx := context.WithValue(context.Background(), webhookCommandKey{}, command)
		request, err := http.NewRequestWithContext(ctx, "POST", "https://api.invalid/v1/webhook_endpoints", strings.NewReader("synthetic body"))
		if err != nil {
			t.Fatal(err)
		}
		request.GetBody = func() (io.ReadCloser, error) {
			t.Fatal("presentation read an unrelated or machine-format request")
			return nil, nil
		}
		if _, err := CaptureTestRequest(request, func(sent *http.Request) (*http.Response, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWebhookRequestContextWithoutReplayBody(t *testing.T) {
	state := new(atomic.Pointer[webhookTestRequest])
	ctx := context.WithValue(context.Background(), webhookCommandKey{}, webhookCommandContext{operation: "test", request: state})
	request, err := http.NewRequestWithContext(ctx, "POST", "https://api.invalid/v1/webhook_endpoints/whe_example/test", io.NopCloser(strings.NewReader("synthetic body")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureTestRequest(request, func(sent *http.Request) (*http.Response, error) {
		got, readErr := io.ReadAll(sent.Body)
		if readErr != nil || string(got) != "synthetic body" {
			t.Fatalf("unreplayable body changed: %q %v", got, readErr)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := state.Load().summary(); !strings.Contains(got, "Project: determined by API key (ID unavailable)") || !strings.Contains(got, "Event: (unavailable)") {
		t.Fatalf("unknown context was inferred: %q", got)
	}
}

type webhookCancelingReader struct {
	cancel        context.CancelFunc
	reads, closes int
}

func (reader *webhookCancelingReader) Read(data []byte) (int, error) {
	reader.reads++
	reader.cancel()
	return copy(data, `{"event_type":"`), nil
}

func (reader *webhookCancelingReader) Close() error {
	reader.closes++
	return nil
}

func TestWebhookRequestContextCancellation(t *testing.T) {
	state := new(atomic.Pointer[webhookTestRequest])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, webhookCommandKey{}, webhookCommandContext{operation: "test", request: state})
	request, err := http.NewRequestWithContext(ctx, "POST", "https://api.invalid/v1/webhook_endpoints/whe_example/test", strings.NewReader(`{"event_type":"response.completed"}`))
	if err != nil {
		t.Fatal(err)
	}
	body := &webhookCancelingReader{cancel: cancel}
	request.GetBody = func() (io.ReadCloser, error) { return body, nil }
	_, err = CaptureTestRequest(request, func(sent *http.Request) (*http.Response, error) {
		return nil, sent.Context().Err()
	})
	if !errors.Is(err, context.Canceled) || body.reads != 1 || body.closes != 1 || state.Load().event != "" {
		t.Fatalf("cancellation was lost: error=%v reads=%d closes=%d context=%+v", err, body.reads, body.closes, state.Load())
	}
}
