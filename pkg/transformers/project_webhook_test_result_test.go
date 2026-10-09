package transformers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const webhookTestResult = `{"object":"webhook_endpoint.test","webhook_endpoint_id":"wh_demo","event_type":"response.completed","status_code":500,"success":true}`

func TestProjectWebhookTestResult(t *testing.T) {
	for _, status := range []int{100, 199, 200, 202, 204, 299, 300, 400, 500, 599} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := strings.Replace(webhookTestResult, "500", fmt.Sprint(status), 1)
			got, err := ProjectWebhookTestResult(context.Background(), gjson.Parse(body))
			acceptance := "failed"
			if status >= 200 && status < 300 {
				acceptance = "accepted"
			}
			want := fmt.Sprintf("Test request completed.\nDelivery %s: endpoint returned HTTP %d.\nWebhook endpoint ID: \"wh_demo\"\nEvent type: \"response.completed\"", acceptance, status)
			if err != nil || got.Type != gjson.String || got.Str != want {
				t.Fatalf("projection: %q, %v; want %q", got.Raw, err, want)
			}
		})
	}
}

func TestProjectWebhookTestResultFallback(t *testing.T) {
	cases := []string{
		`null`, `[]`, `"unexpected"`, `{}`, webhookTestResult[:len(webhookTestResult)-1],
		strings.Replace(webhookTestResult, "webhook_endpoint.test", "other", 1),
		strings.Replace(webhookTestResult, `"success":true`, `"success":true,"success":false`, 1),
		strings.Replace(webhookTestResult, `"success":true`, `"success":true,"\u0073uccess":false`, 1),
		strings.Replace(webhookTestResult, `"success":true`, `"success":true,"future":{"count":9007199254740993}`, 1),
	}
	for _, field := range []string{`"success":true`, `"status_code":500`, `"webhook_endpoint_id":"wh_demo"`, `"event_type":"response.completed"`} {
		parts := strings.SplitN(field, ":", 2)
		for _, replacement := range []string{"null", "false", `"500"`, "[]", "{}"} {
			// A string remains a valid context value.
			if replacement == `"500"` && (parts[0] == `"webhook_endpoint_id"` || parts[0] == `"event_type"`) {
				continue
			}
			cases = append(cases, strings.Replace(webhookTestResult, field, parts[0]+":"+replacement, 1))
		}
		omitted := strings.Replace(webhookTestResult, field+",", "", 1)
		omitted = strings.Replace(omitted, ","+field, "", 1)
		cases = append(cases, omitted)
	}
	for _, status := range []string{"0", "99", "600", "-500", "500.5", "1e99", "99999999999999999999"} {
		cases = append(cases, strings.Replace(webhookTestResult, "500", status, 1))
	}
	for i, body := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			original := gjson.Parse(body)
			got, err := ProjectWebhookTestResult(context.Background(), original)
			if err != nil || got.Raw != original.Raw {
				t.Fatalf("fallback changed data: %q, %v; want %q", got.Raw, err, original.Raw)
			}
		})
	}
}

func TestProjectWebhookTestResultContext(t *testing.T) {
	body := strings.Replace(webhookTestResult, "wh_demo", `wh_\nDelivery accepted.\t\u001b[31m\r\u202e`, 1)
	got, err := ProjectWebhookTestResult(context.Background(), gjson.Parse(body))
	if err != nil || strings.Count(got.Str, "\n") != 3 || strings.ContainsAny(got.Str, "\x1b\r\t\u202e") ||
		!strings.Contains(got.Str, `wh_\nDelivery accepted.\t\x1b[31m\r\u202e`) {
		t.Fatalf("unsafe or missing context: %q, %v", got.Str, err)
	}
}

func TestProjectWebhookTestResultCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ProjectWebhookTestResult(ctx, gjson.Parse(webhookTestResult))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
}

func TestSelectWebhookTestResult(t *testing.T) {
	original := gjson.Parse(webhookTestResult)
	for _, route := range []Route{
		{"(resource) webhooks > (method) test", OutputResponse},
		{"(resource) webhooks > (method) test", OutputPageItem},
		{"(resource) webhooks > (method) test", OutputStreamEvent},
		{"(resource) webhooks > (method) retrieve", OutputResponse},
		{"(resource) webhooks > (method) rotate_secret", OutputResponse},
		{"(resource) other > (method) test", OutputResponse},
	} {
		t.Run(fmt.Sprint(route), func(t *testing.T) {
			got, err := Select(route)(context.Background(), original)
			projected := route == (Route{"(resource) webhooks > (method) test", OutputResponse})
			if err != nil || projected && got.Type != gjson.String || !projected && got.Raw != original.Raw {
				t.Fatalf("incorrect route selection: %q, %v", got.Raw, err)
			}
		})
	}
}
