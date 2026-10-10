package transformers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestSpendThresholdExactUnits(t *testing.T) {
	for _, tc := range []struct{ amount, currency, interval, want string }{
		{"10000", `"USD"`, `"month"`, "USD 100.00 per month"},
		{"0", `"USD"`, `"month"`, "USD 0.00 per month"},
		{"1", `"USD"`, `"month"`, "USD 0.01 per month"},
		{"99", `"USD"`, `"month"`, "USD 0.99 per month"},
		{"-1", `"USD"`, `"month"`, "USD -0.01 per month"},
		{"-10000", `"USD"`, `"month"`, "USD -100.00 per month"},
		{"900719925474099312345678901234567890", `"USD"`, `"month"`, "USD 9007199254740993123456789012345678.90 per month"},
		{"10000", `"EUR"`, `"month"`, "10000 cents (currency: EUR) per month"},
		{"10000", `"USD"`, `"fortnight"`, "USD 100.00 (interval: fortnight)"},
		{"10000", `null`, `null`, "10000 cents (currency: not reported) (interval: not reported)"},
		{"10000", `""`, `""`, "10000 cents (currency: not reported) (interval: not reported)"},
		{"10000", `{}`, `false`, "10000 cents (currency: see response field) (interval: see response field)"},
		{"0.1234567890123456789", `"USD"`, `"month"`, "0.1234567890123456789 cents (currency: USD) per month"},
		{"1e100", `"USD"`, `"month"`, "1e100 cents (currency: USD) per month"},
	} {
		t.Run(tc.amount+tc.currency+tc.interval, func(t *testing.T) {
			got, err := spendThresholdText(t.Context(), tc.amount, gjson.Parse(tc.currency), gjson.Parse(tc.interval))
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestSpendThresholdPreservesResponseFields(t *testing.T) {
	const raw = `{"object":"organization.spend_limit","threshold_amount":10000,"currency":"USD","interval":"month","enforcement":{"status":"future","extra":1.234567890123456789},"future":{"integer":9007199254740993,"decimal":0.000000000000000001}}`
	value, err := projectSpendThreshold(t.Context(), gjson.Parse(raw), "organization.spend_limit", false)
	if err != nil {
		t.Fatal(err)
	}
	if value.Get("spend_threshold").Str != "USD 100.00 per month" || value.Get("threshold_amount").Exists() {
		t.Fatalf("threshold projection: %s", value.Raw)
	}
	for _, field := range []string{"object", "currency", "interval", "enforcement", "future"} {
		if got, want := value.Get(field).Raw, gjson.Get(raw, field).Raw; got != want {
			t.Errorf("%s changed: %s; want %s", field, got, want)
		}
	}
}

func TestSpendThresholdSummaryUsesJSONEscapes(t *testing.T) {
	const raw = `{"object":"organization.spend_limit","threshold_amount":10000,"currency":"USD\u001b[31m","interval":"\u0007\u000b\u0000"}`
	value, err := projectSpendThreshold(t.Context(), gjson.Parse(raw), "organization.spend_limit", false)
	if err != nil || !gjson.Valid(value.Raw) {
		t.Fatalf("control characters broke JSON projection: %q, %v", value.Raw, err)
	}
	want := "10000 cents (currency: USD\x1b[31m) (interval: \a\v\x00)"
	if value.Get("spend_threshold").Str != want {
		t.Fatalf("controls changed before renderer: %q", value.Get("spend_threshold").Str)
	}
}

func TestSpendThresholdMissingFieldsAndCollisions(t *testing.T) {
	for _, raw := range []string{
		`{"object":"organization.spend_limit"}`,
		`{"object":"organization.spend_limit","threshold_amount":null,"enforcement":null}`,
		`{"object":"organization.spend_limit","threshold_amount":"10000"}`,
		`{"object":"organization.spend_limit","threshold_amount":10000,"spend_threshold":"future field"}`,
	} {
		value, err := projectSpendThreshold(t.Context(), gjson.Parse(raw), "organization.spend_limit", false)
		if err != nil || !gjson.Valid(value.Raw) {
			t.Fatalf("invalid projection: %s, %v", value.Raw, err)
		}
		if value.Get("enforcement").Str != "not reported in this response" {
			t.Fatalf("missing enforcement: %s", value.Raw)
		}
		if value.Get("threshold_amount").Raw != gjson.Get(raw, "threshold_amount").Raw {
			t.Fatalf("unexpected amount rewritten: %s", value.Raw)
		}
		if value.Get("spend_threshold").Raw != gjson.Get(raw, "spend_threshold").Raw {
			t.Fatalf("collision overwritten: %s", value.Raw)
		}
	}
	const alert = `{"object":"organization.spend_alert","threshold_amount":0,"alert_behavior":{"future":"kept"}}`
	value, err := projectSpendThreshold(t.Context(), gjson.Parse(alert), "organization.spend_alert", true)
	if err != nil || value.Get("alert_behavior").Raw != `{"future":"kept"}` || value.Get("enforcement").Exists() {
		t.Fatalf("alert collision or invented enforcement: %s, %v", value.Raw, err)
	}
}

func TestSpendThresholdRouteBoundaries(t *testing.T) {
	for _, resource := range []string{"admin.organization", "admin.organization.projects"} {
		for _, tc := range []struct {
			resource, method string
			kind             OutputKind
			match            bool
		}{
			{"spend_limit", "retrieve", OutputResponse, true},
			{"spend_limit", "update", OutputResponse, true},
			{"spend_limit", "delete", OutputResponse, false},
			{"spend_alerts", "create", OutputResponse, true},
			{"spend_alerts", "retrieve", OutputResponse, true},
			{"spend_alerts", "update", OutputResponse, true},
			{"spend_alerts", "list", OutputPageItem, true},
			{"spend_alerts", "list", OutputResponse, false},
			{"spend_alerts", "list", OutputStreamEvent, false},
			{"spend_alerts", "delete", OutputResponse, false},
			{"spend_limit", "retrieve", OutputUnspecified, false},
			{"costs", "list", OutputPageItem, false},
		} {
			route := Route{"(resource) " + resource + "." + tc.resource + " > (method) " + tc.method, tc.kind}
			if got := selectSpendThresholdTransformer(route); (got != nil) != tc.match {
				t.Errorf("route %+v selected=%t, want %t", route, got != nil, tc.match)
			}
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"object":"project.spend_limit","threshold_amount":10000}`, `{"threshold_amount":10000}`} {
		value, err := projectSpendThreshold(t.Context(), gjson.Parse(raw), "organization.spend_limit", false)
		if err != nil || value.Raw != raw {
			t.Fatalf("unrecognized object changed: %s, %v", value.Raw, err)
		}
	}
}

func TestSpendThresholdCancellationAndLargeValues(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := projectSpendThreshold(ctx, gjson.Parse(`{}`), "organization.spend_limit", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := spendCentsDecimal(ctx, strings.Repeat("9", 100000)); !errors.Is(err, context.Canceled) {
		t.Fatalf("amount cancellation lost: %v", err)
	}
	// This exceeds machine integers without adding a presentation rejection limit.
	amount := strings.Repeat("9", 100000)
	got, err := spendCentsDecimal(t.Context(), amount)
	if err != nil || got != amount[:len(amount)-2]+".99" {
		t.Fatalf("large exact amount changed: length=%d, error=%v", len(got), err)
	}
}

func TestSpendThresholdCancelsDuringScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Entry and duplicate-key checks consume six polls before amount scanning.
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 9}
	raw := `{"object":"organization.spend_limit","threshold_amount":` + strings.Repeat("9", 200000) + `,"currency":"USD","interval":"month"}`
	transform := Select(Route{"(resource) admin.organization.spend_limit > (method) retrieve", OutputResponse})
	got, err := transform(controlled, gjson.Parse(raw))
	if !errors.Is(err, context.Canceled) || got.Raw != "" {
		t.Fatalf("cancellation during scan returned partial output: bytes=%d error=%v", len(got.Raw), err)
	}
}

func TestSpendThresholdPreservesDuplicateKeys(t *testing.T) {
	for _, fields := range []string{
		`"threshold_amount":10000,"threshold_amount":90000`,
		`"threshold_amount":10000,"threshold\u005famount":90000`,
		`"threshold_amount":10000,"currency":"USD","currency":"EUR"`,
		`"threshold_amount":10000,"interval":"month","interval":"year"`,
		`"threshold_amount":10000,"enforcement":{"status":"inactive"},"enforcement":{"status":"enforcing"}`,
		`"threshold_amount":10000,"object":"future.spend_limit"`,
		`"threshold_amount":10000,"future":9007199254740993,"future":0.1234567890123456789`,
		`"threshold_amount":10000,"spend_threshold":null,"spend_threshold":"future"`,
		`"threshold_amount":10000,"alert_behavior":null,"alert_behavior":"future"`,
	} {
		for _, object := range []string{"organization.spend_limit", "project.spend_alert"} {
			raw := ` { "object":"` + object + `",` + fields + ` } `
			value := gjson.Parse(raw)
			got, err := projectSpendThreshold(t.Context(), value, object, strings.HasSuffix(object, "spend_alert"))
			if err != nil || got.Raw != value.Raw {
				t.Fatalf("ambiguous object changed: got=%s want=%s error=%v", got.Raw, value.Raw, err)
			}
		}
	}
}

func TestSpendThresholdCancelsDuringDuplicateScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 4}
	value := gjson.Parse(`{"object":"organization.spend_limit","threshold_amount":10000,"currency":"USD","interval":"month"}`)
	got, err := projectSpendThreshold(controlled, value, "organization.spend_limit", false)
	if !errors.Is(err, context.Canceled) || got.Raw != "" {
		t.Fatalf("cancellation during duplicate scan returned partial output: %s error=%v", got.Raw, err)
	}
}
