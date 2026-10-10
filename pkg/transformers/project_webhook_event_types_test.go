package transformers

import (
	"context"
	"errors"
	"testing"

	"github.com/tidwall/gjson"
)

func TestProjectWebhookEventTypes(t *testing.T) {
	value := gjson.Parse(`{"object":"list","data":["response.completed","agent.session.idle","live.transport.incoming","batch.failed","response.failed","future.unknown"]}`)
	got, err := ProjectWebhookEventTypes(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	for group, expected := range map[string]string{
		"background_responses": `["response.completed","response.failed"]`,
		"agent_sessions":       `["agent.session.idle"]`, "batches": `["batch.failed"]`,
		"other": `["live.transport.incoming","future.unknown"]`,
	} {
		if got.Get(group).Raw != expected {
			t.Errorf("%s: got %s, want %s", group, got.Get(group).Raw, expected)
		}
	}
}

func TestProjectWebhookEventTypesFallback(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"object":"list","data":["response.completed"]`,
		`{"object":"list","data":["response.completed"],"has_more":false}`,
		`{"object":"other","data":["response.completed"]}`,
		`{"object":"list","data":[null]}`, `{"object":"list","data":false}`,
		`{"object":"list","data":[],"data":["response.completed"]}`,
	} {
		original := gjson.Parse(body)
		got, err := ProjectWebhookEventTypes(context.Background(), original)
		if err != nil || got.Raw != original.Raw {
			t.Fatalf("fallback changed %q: %q, %v", body, got.Raw, err)
		}
	}
}

func TestProjectWebhookEventTypesEmptyAndCancellation(t *testing.T) {
	got, err := ProjectWebhookEventTypes(context.Background(), gjson.Parse(`{"object":"list","data":[]}`))
	if err != nil || got.Str != "No webhook events are available for this project." {
		t.Fatalf("empty catalog: %q %v", got.Raw, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ProjectWebhookEventTypes(ctx, gjson.Parse(`{"object":"list","data":[]}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
