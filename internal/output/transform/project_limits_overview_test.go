package transform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestProjectLimitsOverviewSpendScopes(t *testing.T) {
	for _, scope := range []string{"organization", "project"} {
		t.Run(scope, func(t *testing.T) {
			raw := `{"scope":"` + scope + `","spend_limit":{"object":"` + scope + `.spend_limit","threshold_amount":9007199254740993,"currency":"USD","interval":"month","enforcement":{"status":"future_status"},"unknown":0.100000000000000001},"spend_limit_configured":true,"spend_alerts":[{"object":"` + scope + `.spend_alert","id":"alert_synthetic","threshold_amount":0,"currency":"USD","interval":"month","notification_channel":{"recipients":["synthetic@example.invalid"],"subject_prefix":null}}],"spend_alerts_complete":false,"source_errors":[],"future":{"value":9007199254740993123456789}}`
			value, err := ProjectLimitsOverview(t.Context(), gjson.Parse(raw))
			if err != nil || !gjson.Valid(value.Raw) {
				t.Fatalf("invalid overview: %s; %v", value.Raw, err)
			}
			if value.Get("spend_limit.spend_threshold").Str != "USD 90071992547409.93 per month" || value.Get("spend_alerts.0.spend_threshold").Str != "USD 0.00 per month" {
				t.Fatalf("incorrect spend units: %s", value.Raw)
			}
			if value.Get("spend_alerts.0.alert_behavior").Str != "Alerts notify; they are not spending caps." {
				t.Fatalf("missing alert behavior: %s", value.Raw)
			}
			for _, field := range []string{"scope", "spend_limit_configured", "spend_alerts_complete", "source_errors", "future", "spend_limit.enforcement", "spend_limit.unknown", "spend_alerts.0.notification_channel"} {
				if got, want := value.Get(field).Raw, gjson.Get(raw, field).Raw; got != want {
					t.Errorf("%s changed: %q; want %q", field, got, want)
				}
			}
		})
	}
}

func TestProjectLimitsOverviewExactRateUnits(t *testing.T) {
	const rate = `{"object":"project.rate_limit","id":"rl_synthetic","model":"synthetic-model","max_requests_per_1_minute":0,"max_tokens_per_1_minute":9007199254740993123456789,"max_images_per_1_minute":-1,"max_audio_megabytes_per_1_minute":0.1234567890123456789,"max_requests_per_1_day":1e9,"batch_1_day_max_input_tokens":9223372036854775807,"future":{"count":9007199254740993}}`
	raw := `{"scope":"project","project_id":"proj_synthetic","project":{"id":"proj_synthetic","future":1e100},"spend_limit":null,"spend_limit_configured":false,"spend_alerts":[],"spend_alerts_complete":true,"project_rate_limits":[` + rate + `],"project_rate_limits_complete":true,"source_errors":[]}`
	value, err := ProjectLimitsOverview(t.Context(), gjson.Parse(raw))
	if err != nil {
		t.Fatal(err)
	}
	projected := value.Get("project_rate_limits.0")
	for label, want := range map[string]string{
		"max_requests_per_minute": "0", "max_tokens_per_minute": "9007199254740993123456789",
		"max_images_per_minute": "-1", "max_audio_megabytes_per_minute": "0.1234567890123456789",
		"max_requests_per_day": "1e9", "max_batch_input_tokens_per_day": "9223372036854775807",
	} {
		if projected.Get(label).Raw != want {
			t.Errorf("rate %s changed units or precision: %s", label, projected.Raw)
		}
	}
	for _, field := range []string{"object", "id", "model", "future"} {
		if projected.Get(field).Raw != gjson.Get(rate, field).Raw {
			t.Errorf("rate field %s changed: %s", field, projected.Raw)
		}
	}
	for _, field := range []string{"project_id", "project", "spend_limit", "spend_limit_configured", "spend_alerts", "spend_alerts_complete", "project_rate_limits_complete", "source_errors"} {
		if value.Get(field).Raw != gjson.Get(raw, field).Raw {
			t.Errorf("overview field %s changed: %s", field, value.Raw)
		}
	}
}

func TestProjectLimitsOverviewRatePresenceAndAmbiguity(t *testing.T) {
	for _, raw := range []string{
		`{"object":"project.rate_limit","id":"rl_synthetic"}`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":null,"max_tokens_per_1_minute":"100"}`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":100,"max_requests_per_minute":null}`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":100,"max_requests_per_1_minute":900}`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":100,"max_requests_per_1_\u006dinute":900}`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":100,"future":1,"future":2}`,
		`{"object":"future.rate_limit","max_requests_per_1_minute":100}`,
		`{"max_requests_per_1_minute":100}`,
		`null`, `[]`, `"future"`,
	} {
		t.Run(raw, func(t *testing.T) {
			value, err := projectLimitsOverviewRate(t.Context(), gjson.Parse(raw))
			if err != nil || value.Raw != raw {
				t.Fatalf("ambiguous or unknown rate changed: %s; %v", value.Raw, err)
			}
		})
	}
}

func TestProjectLimitsOverviewUnrecognizedEnvelope(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{"scope":"future","spend_limit":{}}`,
		`{"scope":"project","scope":"organization","spend_limit":{}}`,
		`{"scope":"project","future":1,"future":2}`,
		`{"scope":"project","spend_alerts":[],"spend_\u0061lerts":null}`,
		`{"scope":null}`, `{"scope":{}}`,
	} {
		value, err := ProjectLimitsOverview(t.Context(), gjson.Parse(raw))
		if err != nil || value.Raw != raw {
			t.Fatalf("unrecognized envelope changed: %s; %v", value.Raw, err)
		}
	}
	const raw = `{"scope":"organization","spend_limit":null,"spend_limit_configured":null,"spend_alerts":null,"project_rate_limits":[{"object":"project.rate_limit","max_requests_per_1_minute":7}],"source_errors":[{"resource":"spend_limit","status":403}]}`
	value, err := ProjectLimitsOverview(t.Context(), gjson.Parse(raw))
	if err != nil || value.Raw != raw {
		t.Fatalf("absent data or project-only rate changed: %s; %v", value.Raw, err)
	}
}

func TestProjectLimitsOverviewKeepsAmbiguousSpendRecords(t *testing.T) {
	const limit = `{"object":"project.spend_limit","threshold_amount":10000,"threshold_amount":90000,"currency":"USD","interval":"month"}`
	const alert = `{"object":"project.spend_alert","threshold_amount":10000,"currency":"USD","currency":"EUR","interval":"month"}`
	raw := `{"scope":"project","spend_limit":` + limit + `,"spend_alerts":[` + alert + `,null,{"object":"future.spend_alert","threshold_amount":10000}]}`
	value, err := ProjectLimitsOverview(t.Context(), gjson.Parse(raw))
	if err != nil || value.Raw != raw {
		t.Fatalf("ambiguous spend or unknown alert changed: %s; %v", value.Raw, err)
	}
}

func TestProjectLimitsOverviewCancellationAndLargeUnknownField(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProjectLimitsOverview(ctx, gjson.Parse(`{"scope":"project"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	unknown := strings.Repeat("x", 1<<20)
	raw := `{"scope":"project","project_rate_limits":[{"object":"project.rate_limit","max_requests_per_1_minute":1,"future":"` + unknown + `"}]}`
	value, err := ProjectLimitsOverview(t.Context(), gjson.Parse(raw))
	if err != nil || value.Get("project_rate_limits.0.future").Str != unknown || value.Get("project_rate_limits.0.max_requests_per_minute").Raw != "1" {
		t.Fatalf("large unknown field lost: length=%d, error=%v", len(value.Raw), err)
	}
}
