package transform

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
		`{"object":"list","data":["fine_tuning.job.succeeded"],"future":true}`,
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

func TestProjectWebhookEventTypesExcludesRetiredEvents(t *testing.T) {
	value := gjson.Parse(`{"object":"list","data":["fine_tuning.job.succeeded","response.completed","fine_tuning.future.event","future.event","custom.fine_tuning.job"]}`)
	got, err := ProjectWebhookEventTypes(context.Background(), value)
	if err != nil || got.Get("background_responses").Raw != `["response.completed"]` ||
		got.Get("other").Raw != `["future.event","custom.fine_tuning.job"]` || got.Get("fine_tuning").Exists() {
		t.Fatalf("retired event filtering lost supported events: %s, %v", got.Raw, err)
	}
	if value.Get("data.0").Str != "fine_tuning.job.succeeded" {
		t.Fatal("readable projection changed the original response")
	}
	got, err = ProjectWebhookEventTypes(context.Background(), gjson.Parse(`{"object":"list","data":["fine_tuning.job.failed"]}`))
	if err != nil || got.Str != "The available webhook events are not supported by the CLI." {
		t.Fatalf("retired-only catalog: %q %v", got.Raw, err)
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
