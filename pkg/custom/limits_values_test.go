package custom

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestLimitsDollarsExactBoundaries(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
	}{
		{"0", 0}, {"0.00", 0}, {"0.01", 1}, {"1.2", 120}, {"00100.01", 10001},
		{"90071992547409.93", 9007199254740993}, {"92233720368547758.07", math.MaxInt64},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := parseLimitsDollars(test.input, 0)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
			roundTrip, err := parseLimitsDollars(formatLimitsDollars(got), 0)
			require.NoError(t, err)
			require.Equal(t, got, roundTrip)
		})
	}
	for _, input := range []string{"", ".01", "1.", "1.001", "1.2.3", "+1", "-0", "-1", "$1", "1,000", "1e2", "NaN", " 1", "1\n", "１２", "92233720368547758.08", "92233720368547759", "18446744073709551616"} {
		t.Run("reject "+input, func(t *testing.T) { _, err := parseLimitsDollars(input, 0); require.Error(t, err) })
	}
	_, err := parseLimitsDollars("0", 1)
	require.Error(t, err)
	require.Equal(t, "-92233720368547758.08", formatLimitsDollars(math.MinInt64))
	require.Equal(t, "-0.01", formatLimitsDollars(-1))
}

func TestLimitsRecipientsDoNotInterpretDisplayNamesOrControls(t *testing.T) {
	got, err := parseLimitsRecipients("finance@example.test, ops+alerts@example.test")
	require.NoError(t, err)
	require.Equal(t, []string{"finance@example.test", "ops+alerts@example.test"}, got)
	for _, input := range []string{"", "finance", "Finance <finance@example.test>", "finance@example.test,", "finance@example.test\r\nBcc: other@example.test", "a@example.test\x1b"} {
		_, err := parseLimitsRecipients(input)
		require.Error(t, err, input)
	}
}

func TestLimitsEditBaselineRejectsAmbiguousOrUnrepresentableConfiguration(t *testing.T) {
	valid := `{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":0,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"]}}`
	require.NoError(t, limitsEditableBaseline(gjson.Parse(valid), "alert_update"))
	for _, raw := range []string{
		`{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":0,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"],"recipients":["other@example.test"]}}`,
		`{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":0,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"],"delivery_policy":{"mode":"unknown"}}}`,
		`{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":9223372036854775808,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"]}}`,
		`{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":1.0,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"]}}`,
		`{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":1,"currency":"EUR","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"]}}`,
		`{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":1,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"],"subject_prefix":false}}`,
	} {
		require.Error(t, limitsEditableBaseline(gjson.Parse(raw), "alert_update"), raw)
	}
	require.Error(t, limitsUniqueJSON(gjson.Parse(`{"records":[{"id":"a","\u0069d":"b"}]}`)))
}

func TestLimitsBaselineComparisonRetainsPrecisionAndIgnoresObjectOrder(t *testing.T) {
	a, err := limitsCanonicalJSON(gjson.Parse(`{"a":9007199254740993,"b":{"x":0.1234567890123456789}}`))
	require.NoError(t, err)
	b, err := limitsCanonicalJSON(gjson.Parse(`{ "b": {"x": 0.1234567890123456789}, "a": 9007199254740993 }`))
	require.NoError(t, err)
	require.Equal(t, a, b)
	c, err := limitsCanonicalJSON(gjson.Parse(`{"a":9007199254740992,"b":{"x":0.1234567890123456789}}`))
	require.NoError(t, err)
	require.NotEqual(t, a, c)
}

func TestLimitsCostsExactDecimalValues(t *testing.T) {
	for _, test := range []struct {
		input, want string
		scale       int
	}{
		{"0.1", "1/10", 1}, {"0.20", "1/5", 2}, {"-0.015", "-3/200", 3},
		{"9007199254740993.000000000000000001", "9007199254740993000000000000000001/1000000000000000000", 18},
		{"1.23e2", "123", 0}, {"1.23e-2", "123/10000", 4}, {"1E+2", "100", 0}, {"-0", "0", 0},
	} {
		t.Run(test.input, func(t *testing.T) {
			value, scale, err := limitsCostDecimal(gjson.Parse(test.input))
			require.NoError(t, err)
			require.Equal(t, test.want, value.RatString())
			require.Equal(t, test.scale, scale)
		})
	}
	for _, input := range []string{"null", `"0.1"`, "true", `{}`, `[]`} {
		_, _, err := limitsCostDecimal(gjson.Parse(input))
		require.Error(t, err)
	}
	for _, input := range []string{
		"1e9223372036854775807", "1e-9223372036854775808", "1e999999999999999999999999999999999",
		"1e10001", "1e-10001", "1." + strings.Repeat("1", 10001), strings.Repeat("1", 10001),
	} {
		_, _, err := limitsCostDecimal(gjson.Parse(input))
		var summary *limitsCostSummaryError
		require.ErrorAs(t, err, &summary)
		require.Equal(t, "unsupported_precision", summary.Reason)
	}
}

func TestLimitsCostsTotalRejectsAmbiguousScopeAndBuckets(t *testing.T) {
	const start, end int64 = 86400, 4 * 86400
	valid := gjson.Parse(limitsTestCostBucket(start, limitsTestCostResult("0.1", "usd", "proj_demo")))
	for _, test := range []struct {
		name, project string
		buckets       []gjson.Result
		reason        string
	}{
		{"overlap", "", []gjson.Result{valid, valid}, "overlapping_buckets"},
		{"outside period", "", []gjson.Result{gjson.Parse(limitsTestCostBucket(0, ""))}, "invalid_bucket"},
		{"non-object", "", []gjson.Result{gjson.Parse(`42`)}, "invalid_bucket"},
		{"wrong project", "proj_other", []gjson.Result{valid}, "unexpected_project"},
		{"malformed results", "", []gjson.Result{gjson.Parse(`{"object":"bucket","start_time":86400,"end_time":172800,"results":null}`)}, "invalid_bucket"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := limitsCostTotal(t.Context(), test.buckets, start, end, test.project)
			var summary *limitsCostSummaryError
			require.ErrorAs(t, err, &summary)
			require.Empty(t, value)
			require.Equal(t, test.reason, summary.Reason)
		})
	}
	value, err := limitsCostTotal(t.Context(), []gjson.Result{valid,
		gjson.Parse(limitsTestCostBucket(start+86400, limitsTestCostResult("-0.3", "USD", "proj_demo")+","+limitsTestCostResult("2e-1", "usd", "proj_demo"))),
	}, start, end, "proj_demo")
	require.NoError(t, err)
	require.Equal(t, "0.00", value)
	value, err = limitsCostTotal(t.Context(), nil, start, end, "")
	require.NoError(t, err)
	require.Equal(t, "0.00", value)
}

func TestLimitsMutationConfirmationMatchesReviewedFields(t *testing.T) {
	change := limitsChange{Kind: "alert_update", ID: "alert_demo", Amount: 12345,
		Recipients: []string{"finance@example.test", "ops@example.test"}, SubjectPrefixSet: true}
	matching := `{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":12345,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["ops@example.test","finance@example.test"],"subject_prefix":null},"future":{"exact":0.1234567890123456789}}`
	require.NoError(t, limitsMutationConfirmed(gjson.Parse(matching), change, false))
	for _, response := range []string{
		strings.Replace(matching, `"threshold_amount":12345`, `"threshold_amount":12344`, 1),
		strings.Replace(matching, `"threshold_amount":12345`, `"threshold_amount":12345.0`, 1),
		strings.Replace(matching, `"currency":"USD"`, `"currency":"EUR"`, 1),
		strings.Replace(matching, `"interval":"month"`, `"interval":"day"`, 1),
		strings.Replace(matching, `"type":"email"`, `"type":"unknown"`, 1),
		strings.Replace(matching, `"ops@example.test"`, `"other@example.test"`, 1),
		strings.Replace(matching, `"ops@example.test","finance@example.test"`, `"finance@example.test","finance@example.test"`, 1),
		strings.Replace(matching, `"subject_prefix":null`, `"subject_prefix":""`, 1),
		strings.Replace(matching, `,"subject_prefix":null`, ``, 1),
		strings.Replace(matching, `"threshold_amount":12345`, `"threshold_amount":12345,"threshold_amount":0`, 1),
	} {
		require.Error(t, limitsMutationConfirmed(gjson.Parse(response), change, false))
	}
	empty := ""
	change.SubjectPrefix = &empty
	require.NoError(t, limitsMutationConfirmed(gjson.Parse(strings.Replace(matching, `"subject_prefix":null`, `"subject_prefix":""`, 1)), change, false))
	change.SubjectPrefixSet = false
	require.NoError(t, limitsMutationConfirmed(gjson.Parse(strings.Replace(matching, `,"subject_prefix":null`, ``, 1)), change, false))
	rateChange := limitsChange{Kind: "rate_update", ID: "rate_demo", Before: gjson.Parse(limitsTestRate), RateValues: map[string]int64{"max_requests_per_1_minute": 9007199254740993}}
	rate := strings.Replace(limitsTestRate, `"max_requests_per_1_minute":100`, `"max_requests_per_1_minute":9007199254740993`, 1)
	require.NoError(t, limitsMutationConfirmed(gjson.Parse(rate), rateChange, true))
	require.Error(t, limitsMutationConfirmed(gjson.Parse(strings.Replace(rate, "9007199254740993", "9007199254740992", 1)), rateChange, true))
	require.Error(t, limitsMutationConfirmed(gjson.Parse(limitsTestRate), rateChange, true))
}

func TestLimitsValueChecksPreserveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, limitsUniqueJSONContext(ctx, gjson.Parse(limitsTestLimit)), context.Canceled)
	require.ErrorIs(t, limitsEditableBaselineContext(ctx, gjson.Parse(limitsTestLimit), "limit_set"), context.Canceled)
	require.ErrorIs(t, limitsValidateChangeContext(ctx, limitsChange{Kind: "limit_set", Amount: 100, BeforeMissing: true}), context.Canceled)
	_, err := limitsCanonicalJSONContext(ctx, gjson.Parse(limitsTestLimit))
	require.ErrorIs(t, err, context.Canceled)
	_, err = limitsCostTotal(ctx, nil, 86400, 172800, "")
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, limitsMutationConfirmedContext(ctx, gjson.Parse(limitsTestLimit), limitsChange{Kind: "limit_set", Amount: 10000}, false), context.Canceled)
}
