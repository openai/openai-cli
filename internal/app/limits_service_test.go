package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const limitsTestLimit = `{"object":"organization.spend_limit","threshold_amount":10000,"currency":"USD","interval":"month","enforcement":{"status":"enforcing"}}`
const limitsTestAlert = `{"id":"alert_demo","object":"organization.spend_alert","threshold_amount":100,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["finance@example.test"]}}`
const limitsTestRate = `{"id":"rate_demo","object":"project.rate_limit","model":"synthetic-model","max_requests_per_1_minute":100,"max_tokens_per_1_minute":200,"future":{"precision":0.1234567890123456789}}`

func limitsTestBackend(t *testing.T, project string, maximum int64, handler http.HandlerFunc) *limitsAPIBackend {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-limits-admin" {
			t.Error("limits service did not select the admin credential")
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return &limitsAPIBackend{client: openai.NewClient(option.WithBaseURL(server.URL),
		option.WithAPIKey("synthetic-limits-project"), option.WithAdminAPIKey("synthetic-limits-admin"), option.WithMaxRetries(0)),
		projectID: project, maxItems: maximum}
}

func limitsTestWrite(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write synthetic response: %v", err)
	}
}

func TestLimitsServicePartialLoadPreservesRawValues(t *testing.T) {
	backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/spend_limit") {
			limitsTestWrite(t, w, `{"object":"organization.spend_limit","threshold_amount":9007199254740993,"future":{"decimal":0.1234567890123456789}}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		limitsTestWrite(t, w, `{"error":{"message":"synthetic-private-field","type":"permission_error"}}`)
	})
	snapshot, err := backend.Load(t.Context())
	require.Error(t, err)
	require.False(t, snapshot.AlertsComplete)
	value, err := snapshot.JSON()
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", value.Get("spend_limit.threshold_amount").Raw)
	require.Equal(t, "0.1234567890123456789", value.Get("spend_limit.future.decimal").Raw)
	require.Equal(t, "api_error", value.Get("source_errors.0.status").Str)
	require.Equal(t, int64(403), value.Get("source_errors.0.status_code").Int())
	require.NotContains(t, value.Raw, "synthetic-private-field")
	require.Equal(t, "[]", value.Get("spend_alerts").Raw)
}

func TestLimitsServiceDoesNotInferLimitAbsenceAfterProjectFailure(t *testing.T) {
	var limitRequests atomic.Int32
	backend := limitsTestBackend(t, "proj_demo", 100, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/proj_demo") {
			w.WriteHeader(http.StatusNotFound)
			limitsTestWrite(t, w, `{"error":{"message":"synthetic missing project"}}`)
		} else if strings.HasSuffix(r.URL.Path, "/spend_limit") {
			limitRequests.Add(1)
			w.WriteHeader(http.StatusNotFound)
		} else {
			limitsTestWrite(t, w, `{"object":"list","data":[],"has_more":false}`)
		}
	})
	snapshot, err := backend.Load(t.Context())
	require.Error(t, err)
	require.Zero(t, limitRequests.Load())
	require.False(t, snapshot.LimitAbsent)
	value, err := snapshot.JSON()
	require.NoError(t, err)
	require.Equal(t, "null", value.Get("spend_limit_configured").Raw)
}

func TestLimitsServicePaginationCapsAndCycle(t *testing.T) {
	for _, test := range []struct {
		maximum           int64
		count             int
		complete, failure bool
		requests          int32
	}{
		{0, 0, false, false, 1}, {1, 1, false, false, 1}, {-1, 2, false, true, 2},
	} {
		t.Run(fmt.Sprint(test.maximum), func(t *testing.T) {
			var requests atomic.Int32
			backend := limitsTestBackend(t, "", test.maximum, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if test.maximum == 0 && r.URL.Query().Get("limit") != "1" {
					t.Error("zero cap must inspect one-record page")
				}
				limitsTestWrite(t, w, `{"object":"list","data":[`+limitsTestAlert+`],"has_more":true,"last_id":"alert_demo"}`)
			})
			items, complete, err := backend.list(t.Context(), false)
			require.Len(t, items, test.count)
			require.Equal(t, test.complete, complete)
			require.Equal(t, test.failure, err != nil)
			require.Equal(t, test.requests, requests.Load())
		})
	}
}

func TestLimitsServiceListRequiresMatchingObject(t *testing.T) {
	for _, scope := range []struct {
		name, project, object, other string
		rates                        bool
	}{
		{"organization_alerts", "", "organization.spend_alert", "project.spend_alert", false},
		{"project_alerts", "proj_demo", "project.spend_alert", "organization.spend_alert", false},
		{"project_rates", "proj_demo", "project.rate_limit", "project.spend_alert", true},
	} {
		t.Run(scope.name, func(t *testing.T) {
			first := fmt.Sprintf(`{"object":%q,"id":"first","future":1,"future":2}`, scope.object)
			second := fmt.Sprintf(`{"object":%q,"id":"second","future":{"exact":9007199254740993},"optional":null}`, scope.object)
			for _, tc := range []struct{ name, fields string }{
				{"wrong_scope", fmt.Sprintf(`"object":%q`, scope.other)},
				{"missing", `"future":true`},
				{"null", `"object":null`},
				{"number", `"object":1`},
				{"duplicate_same", fmt.Sprintf(`"object":%q,"object":%q`, scope.object, scope.object)},
				{"duplicate_other", fmt.Sprintf(`"object":%q,"object":%q`, scope.object, scope.other)},
				{"duplicate_escaped", fmt.Sprintf(`"object":%q,"\u006fbject":%q`, scope.object, scope.object)},
				{"valid_sparse_future_duplicates", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var reads, writes atomic.Int32
					backend := limitsTestBackend(t, scope.project, -1, func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet {
							writes.Add(1)
							t.Error("list sent a mutation")
						}
						reads.Add(1)
						if r.URL.Query().Get("after") == "" {
							limitsTestWrite(t, w, `{"object":"list","data":[`+first+`],"has_more":true,"last_id":"first"}`)
							return
						}
						if r.URL.Query().Get("after") != "first" {
							t.Error("list did not preserve its pagination cursor")
						}
						data := second
						if tc.fields != "" {
							data += `,{"id":"rejected",` + tc.fields + `},` + strings.Replace(second, "second", "after_rejection", 1)
						}
						limitsTestWrite(t, w, `{"object":"list","data":[`+data+`],"has_more":false}`)
					})
					items, complete, err := backend.list(t.Context(), scope.rates)
					require.Len(t, items, 2)
					require.Equal(t, []string{first, second}, []string{items[0].Raw, items[1].Raw})
					require.Equal(t, tc.fields == "", complete)
					if tc.fields == "" {
						require.NoError(t, err)
					} else {
						require.EqualError(t, err, "A limits page contains an unexpected resource or scope.")
					}
					require.Equal(t, int32(2), reads.Load())
					require.Zero(t, writes.Load())
					m, state := newLimitsController(t, 40, 12)
					m.section, m.snapshot.Alerts = 1, items
					if scope.rates {
						m.section, m.snapshot.Rates = 2, items
					}
					for _, row := range m.menu() {
						require.NotContains(t, row.label, "rejected")
						require.NotContains(t, row.label, "after_rejection")
					}
					require.Zero(t, state.writes.Load())
				})
			}
		})
	}
}

func TestLimitsServiceListRequiresListWrapper(t *testing.T) {
	for _, scope := range []struct {
		name, project, record string
		rates                 bool
	}{
		{"organization_alerts", "", limitsTestAlert, false},
		{"project_alerts", "proj_demo", strings.Replace(limitsTestAlert, "organization.", "project.", 1), false},
		{"project_rates", "proj_demo", limitsTestRate, true},
	} {
		for _, wrapper := range []struct{ name, fields string }{
			{"missing", `"future":true`},
			{"wrong", `"object":"page"`},
			{"null", `"object":null`},
			{"number", `"object":1`},
			{"duplicate", `"object":"list","object":"list"`},
			{"escaped_duplicate", `"object":"list","\u006fbject":"list"`},
			{"valid", `"object":"list","future":{"exact":9007199254740993}`},
		} {
			t.Run(scope.name+"/"+wrapper.name, func(t *testing.T) {
				var reads atomic.Int32
				var zeroItems atomic.Bool
				backend := limitsTestBackend(t, scope.project, -1, func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Errorf("list sent a mutation: %s", r.Method)
					}
					reads.Add(1)
					if !zeroItems.Load() && r.URL.Query().Get("after") == "" {
						limitsTestWrite(t, w, `{"object":"list","data":[`+scope.record+`],"has_more":true,"last_id":"first"}`)
						return
					}
					if !zeroItems.Load() && r.URL.Query().Get("after") != "first" {
						t.Error("list changed its pagination cursor")
					}
					limitsTestWrite(t, w, `{`+wrapper.fields+`,"data":[`+scope.record+`],"has_more":false}`)
				})
				items, complete, err := backend.list(t.Context(), scope.rates)
				if wrapper.name == "valid" {
					require.NoError(t, err)
					require.True(t, complete)
					require.Len(t, items, 2)
				} else {
					require.Error(t, err)
					require.False(t, complete)
					require.Len(t, items, 1, "retain only records from the accepted first page")
				}
				require.Equal(t, scope.record, items[0].Raw)
				require.Equal(t, int32(2), reads.Load())
				backend.maxItems = 0
				zeroItems.Store(true)
				items, complete, err = backend.list(t.Context(), scope.rates)
				require.Equal(t, wrapper.name != "valid", err != nil)
				require.False(t, complete)
				require.Empty(t, items)
				require.Equal(t, int32(3), reads.Load())
			})
		}
	}
}

func TestLimitsServiceRejectsStaleLimitAndDuplicateBaselineWithoutWriting(t *testing.T) {
	var writes atomic.Int32
	backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		limitsTestWrite(t, w, strings.Replace(limitsTestLimit, "10000", "20000", 1))
	})
	_, err := backend.Mutate(t.Context(), limitsChange{Kind: "limit_set", Amount: 30000, Before: gjson.Parse(limitsTestLimit)})
	require.ErrorContains(t, err, "configuration changed")
	var failure *limitsMutationFailure
	require.ErrorAs(t, err, &failure)
	require.False(t, failure.Attempted)
	duplicate := strings.Replace(limitsTestAlert, `"type":"email"`, `"type":"email","type":"email"`, 1)
	_, err = backend.Mutate(t.Context(), limitsChange{Kind: "alert_update", ID: "alert_demo", Amount: 300,
		Recipients: []string{"finance@example.test"}, Before: gjson.Parse(duplicate)})
	require.ErrorContains(t, err, "duplicate")
	require.Zero(t, writes.Load())
}

func TestLimitsServiceAlertPrefixPresenceAndNoMutationRetries(t *testing.T) {
	empty, subject := "", "synthetic subject"
	for _, test := range []struct {
		name  string
		set   bool
		value *string
		want  string
	}{
		{"absent", false, nil, ""}, {"null", true, nil, "null"}, {"empty", true, &empty, `""`}, {"text", true, &subject, `"synthetic subject"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var writes atomic.Int32
			var sent []byte
			backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					limitsTestWrite(t, w, limitsTestAlert)
					return
				}
				writes.Add(1)
				var err error
				sent, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				w.WriteHeader(http.StatusInternalServerError)
				limitsTestWrite(t, w, `{"error":{"message":"synthetic failed write"}}`)
			})
			backend.client = openai.NewClient(append(backend.client.Options, option.WithMaxRetries(3))...)
			_, err := backend.Mutate(t.Context(), limitsChange{Kind: "alert_update", ID: "alert_demo", Before: gjson.Parse(limitsTestAlert),
				Amount: 0, Recipients: []string{"ops@example.test"}, SubjectPrefixSet: test.set, SubjectPrefix: test.value})
			require.Error(t, err)
			var failure *limitsMutationFailure
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.Attempted)
			require.Equal(t, int32(1), writes.Load())
			value := gjson.ParseBytes(sent)
			require.Equal(t, "0", value.Get("threshold_amount").Raw)
			require.Equal(t, test.want, value.Get("notification_channel.subject_prefix").Raw)
		})
	}
}

func TestLimitsServiceRateUpdatePreservesZeroPrecisionAndUnknownResponseFields(t *testing.T) {
	var body []byte
	baseline := strings.Replace(limitsTestRate, `"future":`, `"max_requests_per_1_day":1,"max_images_per_1_minute":2,"max_audio_megabytes_per_1_minute":3,"batch_1_day_max_input_tokens":4,"max_tokens_per_1_day":5,"future":`, 1)
	backend := limitsTestBackend(t, "proj_demo", 100, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/proj_demo") {
			limitsTestWrite(t, w, `{"id":"proj_demo","object":"organization.project"}`)
		} else if r.Method == http.MethodGet {
			limitsTestWrite(t, w, `{"object":"list","data":[`+baseline+`],"has_more":false}`)
		} else {
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var updated map[string]json.RawMessage
			var changed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(baseline), &updated); err != nil {
				t.Error(err)
			}
			if err := json.Unmarshal(body, &changed); err != nil {
				t.Error(err)
			}
			for key, value := range changed {
				updated[key] = value
			}
			encoded, err := json.Marshal(updated)
			if err != nil {
				t.Error(err)
			}
			limitsTestWrite(t, w, string(encoded))
		}
	})
	value, err := backend.Mutate(t.Context(), limitsChange{Kind: "rate_update", ID: "rate_demo", Before: gjson.Parse(baseline),
		RateValues: map[string]int64{"max_requests_per_1_minute": 0, "max_tokens_per_1_minute": 201,
			"max_requests_per_1_day": 123, "max_images_per_1_minute": 4,
			"max_audio_megabytes_per_1_minute": 5, "batch_1_day_max_input_tokens": 9007199254740993,
			"max_tokens_per_1_day": 9223372036854775807}})
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", gjson.GetBytes(body, "batch_1_day_max_input_tokens").Raw)
	require.JSONEq(t, `{"max_requests_per_1_minute":0,"max_tokens_per_1_minute":201,"max_requests_per_1_day":123,"max_images_per_1_minute":4,"max_audio_megabytes_per_1_minute":5,"batch_1_day_max_input_tokens":9007199254740993,"max_tokens_per_1_day":9223372036854775807}`, string(body))
	require.Equal(t, "9223372036854775807", gjson.GetBytes(body, "max_tokens_per_1_day").Raw)
	require.Equal(t, "0.1234567890123456789", value.Get("future.precision").Raw)
}

func TestLimitsServiceCancellationAndRepeatedSubmission(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			limitsTestWrite(t, w, limitsTestLimit)
			return
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	change := limitsChange{Kind: "limit_set", Amount: 20000, Before: gjson.Parse(limitsTestLimit)}
	finished := make(chan error, 1)
	go func() { _, err := backend.Mutate(ctx, change); finished <- err }()
	<-started
	_, err := backend.Mutate(t.Context(), change)
	require.ErrorContains(t, err, "already in progress")
	cancel()
	close(release)
	require.ErrorIs(t, <-finished, context.Canceled)
}

func TestLimitsSnapshotErrorEnvelopeNeverIncludesProviderText(t *testing.T) {
	snapshot := limitsSnapshot{Errors: []limitsSourceFailure{
		{Resource: "spend_limit", Err: context.Canceled},
		{Resource: "spend_alerts", Err: errors.New("synthetic-private-url")},
	}}
	value, err := snapshot.JSON()
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(value.Raw)))
	require.Equal(t, "canceled", value.Get("source_errors.0.status").Str)
	require.Equal(t, "request_failed", value.Get("source_errors.1.status").Str)
	require.NotContains(t, value.Raw, "synthetic-private-url")
}

func TestLimitsBackendConstructorPreservesRequestContextAndTLSClient(t *testing.T) {
	requests := make(chan http.Header, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/spend_limit") {
			limitsTestWrite(t, w, limitsTestLimit)
		} else if strings.HasSuffix(r.URL.Path, "/costs") {
			limitsTestWrite(t, w, `{"object":"page","data":[],"has_more":false,"next_page":null}`)
		} else {
			limitsTestWrite(t, w, `{"object":"list","data":[],"has_more":false}`)
		}
	}))
	t.Cleanup(server.Close)
	root := &cli.Command{Name: "openai", Metadata: map[string]any{requestconfig.MTLSHTTPClientMetadata: server.Client()},
		Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}, &cli.StringFlag{Name: "api-key"},
			&cli.StringFlag{Name: "admin-api-key"}, &cli.StringFlag{Name: "organization"},
			&cli.StringFlag{Name: "project"}, NewRequestHeaderFlag(), &cli.Int64Flag{Name: "max-items", Value: 100}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			backend, err := newLimitsBackend(cmd, false)
			if err != nil {
				return err
			}
			_, err = backend.Load(ctx)
			return err
		}}
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--base-url", server.URL,
		"--api-key", "synthetic-project-key", "--admin-api-key", "synthetic-admin-key",
		"--organization", "org_demo", "--project", "proj_header", "--header", "X-Limits-Demo: preserved"}))
	for range 3 {
		header := <-requests
		require.Equal(t, "Bearer synthetic-admin-key", header.Get("Authorization"))
		require.Equal(t, "org_demo", header.Get("OpenAI-Organization"))
		require.Equal(t, "proj_header", header.Get("OpenAI-Project"))
		require.Equal(t, "preserved", header.Get("X-Limits-Demo"))
	}
}

func TestLimitsServiceRefusesIncompleteRatePreflight(t *testing.T) {
	var writes atomic.Int32
	backend := limitsTestBackend(t, "proj_demo", 1, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/proj_demo") {
			limitsTestWrite(t, w, `{"id":"proj_demo","object":"organization.project"}`)
		} else {
			limitsTestWrite(t, w, `{"object":"list","data":[`+limitsTestRate+`],"has_more":true,"last_id":"rate_demo"}`)
		}
	})
	_, err := backend.Mutate(t.Context(), limitsChange{Kind: "rate_update", ID: "rate_demo", Before: gjson.Parse(limitsTestRate),
		RateValues: map[string]int64{"max_requests_per_1_minute": 1}})
	require.ErrorContains(t, err, "complete list")
	var failure *limitsMutationFailure
	require.ErrorAs(t, err, &failure)
	require.False(t, failure.Attempted)
	require.Zero(t, writes.Load())
}

func TestLimitsServiceMutationOperationsUseTypedRoutes(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, kind := range []string{"limit_set", "limit_delete", "alert_create", "alert_update", "alert_delete"} {
			t.Run(fmt.Sprintf("%t/%s", project, kind), func(t *testing.T) {
				projectID, scope, prefix := "", "organization", "/organization"
				if project {
					projectID, scope, prefix = "proj_demo", "project", "/organization/projects/proj_demo"
				}
				limit := strings.ReplaceAll(limitsTestLimit, "organization.spend_limit", scope+".spend_limit")
				alert := strings.ReplaceAll(limitsTestAlert, "organization.spend_alert", scope+".spend_alert")
				type request struct{ method, path, body string }
				sent := make(chan request, 1)
				backend := limitsTestBackend(t, projectID, 100, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						switch r.URL.Path {
						case "/organization/projects/proj_demo":
							limitsTestWrite(t, w, `{"id":"proj_demo","object":"organization.project"}`)
						case prefix + "/spend_limit":
							limitsTestWrite(t, w, limit)
						case prefix + "/spend_alerts/alert_demo":
							limitsTestWrite(t, w, alert)
						default:
							t.Errorf("unexpected read: %s", r.URL.Path)
							w.WriteHeader(400)
						}
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					sent <- request{r.Method, r.URL.Path, string(body)}
					switch kind {
					case "limit_set":
						limitsTestWrite(t, w, strings.Replace(limit, `"threshold_amount":10000`, `"threshold_amount":12345`, 1))
					case "alert_create", "alert_update":
						limitsTestWrite(t, w, strings.Replace(alert, `"threshold_amount":100`, `"threshold_amount":12345`, 1))
					case "limit_delete":
						limitsTestWrite(t, w, `{"object":"`+scope+`.spend_limit.deleted","deleted":true}`)
					case "alert_delete":
						limitsTestWrite(t, w, `{"id":"alert_demo","object":"`+scope+`.spend_alert.deleted","deleted":true}`)
					}
				})
				change := limitsChange{Kind: kind, Amount: 12345, Before: gjson.Parse(limit)}
				path, method := prefix+"/spend_limit", http.MethodPost
				if strings.HasPrefix(kind, "alert_") {
					change.Before, change.ID = gjson.Parse(alert), "alert_demo"
					change.Recipients = []string{"finance@example.test"}
					path = prefix + "/spend_alerts/alert_demo"
					if kind == "alert_create" {
						path = prefix + "/spend_alerts"
						change.Before, change.ID = gjson.Result{}, ""
					}
				}
				if strings.HasSuffix(kind, "_delete") {
					method = http.MethodDelete
				}
				_, err := backend.Mutate(t.Context(), change)
				require.NoError(t, err)
				got := <-sent
				require.Equal(t, method, got.method)
				require.Equal(t, path, got.path)
				if method == http.MethodPost {
					require.Equal(t, "12345", gjson.Get(got.body, "threshold_amount").Raw)
					require.Equal(t, "USD", gjson.Get(got.body, "currency").Str)
					require.Equal(t, "month", gjson.Get(got.body, "interval").Str)
				} else {
					require.Empty(t, got.body)
				}
			})
		}
	}
}

func TestLimitsServiceIncompleteMutationConfirmationIsUncertain(t *testing.T) {
	for _, test := range []struct{ kind, response string }{
		{"limit_set", `{}`},
		{"limit_set", `{"object":"project.spend_limit","threshold_amount":100,"currency":"USD","interval":"month"}`},
		{"alert_create", `{"object":"organization.spend_alert","threshold_amount":100,"currency":"USD","interval":"month"}`},
		{"alert_update", strings.ReplaceAll(limitsTestAlert, "alert_demo", "alert_other")},
		{"limit_delete", `{"object":"organization.spend_limit.deleted","deleted":false}`},
		{"alert_delete", `{"id":"alert_other","object":"organization.spend_alert.deleted","deleted":true}`},
	} {
		t.Run(test.kind+test.response, func(t *testing.T) {
			var writes atomic.Int32
			backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					limitsTestWrite(t, w, test.response)
					return
				}
				if strings.Contains(r.URL.Path, "spend_alerts") {
					limitsTestWrite(t, w, limitsTestAlert)
				} else {
					limitsTestWrite(t, w, limitsTestLimit)
				}
			})
			change := limitsChange{Kind: test.kind, Amount: 100, Before: gjson.Parse(limitsTestLimit)}
			if strings.HasPrefix(test.kind, "alert_") {
				change.Before, change.ID, change.Recipients = gjson.Parse(limitsTestAlert), "alert_demo", []string{"finance@example.test"}
			}
			_, err := backend.Mutate(t.Context(), change)
			require.ErrorContains(t, err, "incomplete confirmation")
			var failure *limitsMutationFailure
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.Attempted)
			require.Equal(t, int32(1), writes.Load())
		})
	}
}

func TestLimitsServiceWrongDiscriminatorRemainsUnavailable(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprint(project), func(t *testing.T) {
			projectID := ""
			if project {
				projectID = "proj_demo"
			}
			var limitRequests atomic.Int32
			backend := limitsTestBackend(t, projectID, 100, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/proj_demo"):
					limitsTestWrite(t, w, `{"id":"proj_demo","object":"project.spend_limit","future":123}`)
				case strings.HasSuffix(r.URL.Path, "/spend_limit"):
					limitRequests.Add(1)
					limitsTestWrite(t, w, `{"object":"project.spend_limit","threshold_amount":10000,"future":123}`)
				default:
					limitsTestWrite(t, w, `{"object":"list","data":[],"has_more":false}`)
				}
			})
			snapshot, err := backend.Load(t.Context())
			require.Error(t, err)
			value, err := snapshot.JSON()
			require.NoError(t, err)
			require.Equal(t, "null", value.Get("spend_limit_configured").Raw)
			require.Equal(t, "request_failed", value.Get("source_errors.0.status").Str)
			if project {
				require.Zero(t, limitRequests.Load())
				require.Equal(t, "project", value.Get("source_errors.0.resource").Str)
				require.Equal(t, "123", value.Get("project.future").Raw)
			} else {
				require.Equal(t, int32(1), limitRequests.Load())
				require.Equal(t, "spend_limit", value.Get("source_errors.0.resource").Str)
				require.Equal(t, "123", value.Get("spend_limit.future").Raw)
			}
		})
	}
}

func TestLimitsSnapshotProjectFailureOverridesPriorLimitPresence(t *testing.T) {
	for _, absent := range []bool{false, true} {
		snapshot := limitsSnapshot{
			ProjectID: "proj_demo", Limit: gjson.Parse(limitsTestLimit), LimitAbsent: absent,
			Errors: []limitsSourceFailure{{Resource: "project", Err: errors.New("synthetic project failure")}},
		}
		value, err := snapshot.JSON()
		require.NoError(t, err)
		require.Equal(t, "null", value.Get("spend_limit_configured").Raw)
		require.Equal(t, "10000", value.Get("spend_limit.threshold_amount").Raw)
	}
}

func TestLimitsBackendValidationUsesSafeLocalErrors(t *testing.T) {
	for _, test := range []struct {
		project bool
		args    []string
		message string
	}{
		{false, []string{"openai", "--max-items=-2"}, "--max-items must be -1, zero, or a positive integer."},
		{true, []string{"openai"}, "Set --project-id to inspect project limits."},
	} {
		var validationErr error
		root := &cli.Command{
			Name: "openai", Flags: []cli.Flag{&cli.Int64Flag{Name: "max-items", Value: 100}, &cli.StringFlag{Name: "project-id"}},
			Action: func(_ context.Context, cmd *cli.Command) error {
				_, validationErr = newLimitsBackend(cmd, test.project)
				return nil
			},
		}
		require.NoError(t, root.Run(t.Context(), test.args))
		var localErr *localcommands.LocalError
		require.ErrorAs(t, validationErr, &localErr)
		require.EqualError(t, validationErr, test.message)
	}
}

func limitsTestCostsBackend(t *testing.T, project string, maximum int64, costs http.HandlerFunc) *limitsAPIBackend {
	t.Helper()
	backend := limitsTestBackend(t, project, maximum, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/costs"):
			costs(w, r)
		case strings.HasSuffix(r.URL.Path, "/proj_demo"):
			limitsTestWrite(t, w, `{"id":"proj_demo","object":"organization.project"}`)
		case strings.HasSuffix(r.URL.Path, "/spend_limit"):
			limit := limitsTestLimit
			if project != "" {
				limit = strings.ReplaceAll(limit, "organization.spend_limit", "project.spend_limit")
			}
			limitsTestWrite(t, w, limit)
		default:
			limitsTestWrite(t, w, `{"object":"list","data":[],"has_more":false}`)
		}
	})
	backend.now = func() time.Time { return time.Date(2026, 10, 19, 12, 30, 0, 0, time.UTC) }
	return backend
}

func limitsTestCostBucket(start int64, results string) string {
	return fmt.Sprintf(`{"object":"bucket","start_time":%d,"end_time":%d,"results":[%s],"future":{"exact":0.1234567890123456789}}`, start, start+86400, results)
}

func limitsTestCostResult(amount, currency, project string) string {
	return fmt.Sprintf(`{"object":"organization.costs.result","amount":{"value":%s,"currency":%q},"project_id":%q}`, amount, currency, project)
}

func TestLimitsCostsExactPaginationAndUTCProjectScope(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).Unix()
	for _, project := range []string{"", "proj_demo"} {
		t.Run("project="+project, func(t *testing.T) {
			var calls atomic.Int32
			backend := limitsTestCostsBackend(t, project, 100, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("start_time") != fmt.Sprint(start) ||
					r.URL.Query().Get("end_time") != fmt.Sprint(end) || r.URL.Query().Get("bucket_width") != "1d" {
					t.Error("Costs request changed its method or UTC period")
				}
				if got := r.URL.Query().Get("project_ids[]"); got != project {
					t.Errorf("project filter = %q, want %q", got, project)
				}
				if r.URL.Query().Get("group_by[]") != "" {
					t.Error("Costs summary unexpectedly grouped results")
				}
				if r.URL.Query().Get("page") == "" {
					bucket := limitsTestCostBucket(start, limitsTestCostResult("9007199254740993.000000000000000001", "usd", project))
					limitsTestWrite(t, w, `{"object":"page","data":[`+bucket+`],"has_more":true,"next_page":"next_demo"}`)
				} else {
					if r.URL.Query().Get("page") != "next_demo" {
						t.Error("Costs did not retain its cursor")
					}
					bucket := limitsTestCostBucket(start+86400, limitsTestCostResult("2e-1", "USD", project)+","+limitsTestCostResult("-0.1", "usd", project))
					limitsTestWrite(t, w, `{"object":"page","data":[`+bucket+`],"has_more":false,"next_page":null}`)
				}
			})
			snapshot, err := backend.Load(t.Context())
			require.NoError(t, err)
			require.True(t, snapshot.CostsComplete)
			require.Equal(t, "9007199254740993.100000000000000001", snapshot.CostsTotal)
			require.Equal(t, int32(2), calls.Load())
			value, err := snapshot.JSON()
			require.NoError(t, err)
			require.Equal(t, start, value.Get("costs.start_time").Int())
			require.Equal(t, end, value.Get("costs.end_time").Int())
			require.Equal(t, "UTC", value.Get("costs.timezone").Str)
			require.Equal(t, snapshot.CostsTotal, value.Get("costs.total").Str)
			require.Equal(t, "0.1234567890123456789", value.Get("costs.buckets.0.future.exact").Raw)
		})
	}
}

func TestLimitsCostsBoundsAndLaterFailuresPreservePartialData(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, test := range []struct {
		name      string
		maximum   int64
		mode      string
		calls     int32
		wantTotal string
		wantError bool
	}{
		{"zero", 0, "", 1, "", false},
		{"bound", 1, "", 1, "0.10", false},
		{"later HTTP error", -1, "failure", 2, "0.10", true},
		{"cursor cycle", -1, "cycle", 2, "0.30", true},
		{"missing cursor", -1, "missing", 1, "0.10", true},
		{"empty continuation", -1, "empty", 2, "0.10", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			backend := limitsTestCostsBackend(t, "", test.maximum, func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				if test.maximum == 0 && r.URL.Query().Get("limit") != "1" {
					t.Error("zero maximum must make a one-bucket request")
				}
				if call == 2 && test.mode == "failure" {
					w.WriteHeader(http.StatusForbidden)
					limitsTestWrite(t, w, `{"error":{"message":"synthetic-private-cost-detail"}}`)
					return
				}
				cursor := `"next_demo"`
				if test.mode == "missing" {
					cursor = "null"
				}
				data := limitsTestCostBucket(start+int64(call-1)*86400, limitsTestCostResult(fmt.Sprintf("0.%d", call), "usd", ""))
				if call == 2 && test.mode == "empty" {
					data = ""
				}
				limitsTestWrite(t, w, `{"object":"page","data":[`+data+`],"has_more":true,"next_page":`+cursor+`}`)
			})
			snapshot, err := backend.Load(t.Context())
			require.Equal(t, test.wantError, err != nil)
			require.Equal(t, test.calls, calls.Load())
			require.False(t, snapshot.CostsComplete)
			require.Equal(t, test.wantTotal, snapshot.CostsTotal)
			require.True(t, snapshot.Limit.Exists())
			value, jsonErr := snapshot.JSON()
			require.NoError(t, jsonErr)
			require.NotContains(t, value.Raw, "synthetic-private-cost-detail")
			if test.mode == "failure" {
				require.Equal(t, "api_error", value.Get("source_errors.0.status").Str)
				require.Equal(t, int64(403), value.Get("source_errors.0.status_code").Int())
			}
		})
	}
}

func TestLimitsCostsSummaryFailuresRetainRawBuckets(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, test := range []struct{ name, result, reason string }{
		{"huge exponent", limitsTestCostResult("1e9223372036854775807", "usd", ""), "unsupported_precision"},
		{"overflow exponent", limitsTestCostResult("1e999999999999999999999999999999", "usd", ""), "unsupported_precision"},
		{"currency", limitsTestCostResult("0.1", "EUR", ""), "unsupported_currency"},
		{"missing amount", `{"object":"organization.costs.result","amount":{"currency":"usd"}}`, "invalid_amount"},
		{"duplicate amount", `{"object":"organization.costs.result","amount":{"currency":"usd","value":0.1,"value":0.2}}`, "ambiguous_fields"},
		{"wrong result", `{"object":"organization.usage.completions.result","amount":{"currency":"usd","value":0.1}}`, "unexpected_result"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bucket := limitsTestCostBucket(start, test.result)
			backend := limitsTestCostsBackend(t, "", 100, func(w http.ResponseWriter, _ *http.Request) {
				limitsTestWrite(t, w, `{"object":"page","data":[`+bucket+`],"has_more":false,"next_page":null}`)
			})
			snapshot, err := backend.Load(t.Context())
			var summary *limitsCostSummaryError
			require.ErrorAs(t, err, &summary)
			require.Equal(t, test.reason, summary.Reason)
			require.ErrorContains(t, err, "costs.buckets")
			require.False(t, snapshot.CostsComplete)
			require.Empty(t, snapshot.CostsTotal)
			require.Equal(t, bucket, snapshot.Costs[0].Raw)
			value, err := snapshot.JSON()
			require.NoError(t, err)
			require.Equal(t, "null", value.Get("costs.total").Raw)
			require.Equal(t, "summary_unavailable", value.Get("source_errors.0.status").Str)
			require.Equal(t, test.reason, value.Get("source_errors.0.summary_reason").Str)
			require.True(t, value.Get("spend_limit_configured").Bool())
		})
	}
}

func TestLimitsUnknown404RemainsUnconfirmed(t *testing.T) {
	for _, project := range []string{"", "proj_demo"} {
		t.Run("project="+project, func(t *testing.T) {
			backend := limitsTestBackend(t, project, 100, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/proj_demo"):
					limitsTestWrite(t, w, `{"id":"proj_demo","object":"organization.project"}`)
				case strings.HasSuffix(r.URL.Path, "/spend_limit"):
					w.WriteHeader(http.StatusNotFound)
					limitsTestWrite(t, w, `{"error":{"code":"not_found","message":"synthetic private unavailable state"}}`)
				case strings.HasSuffix(r.URL.Path, "/costs"):
					limitsTestWrite(t, w, `{"object":"page","data":[],"has_more":false,"next_page":null}`)
				default:
					limitsTestWrite(t, w, `{"object":"list","data":[],"has_more":false}`)
				}
			})
			snapshot, err := backend.Load(t.Context())
			var apiErr *openai.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
			require.False(t, snapshot.LimitAbsent)
			require.True(t, snapshot.LimitUnconfirmed)
			value, err := snapshot.JSON()
			require.NoError(t, err)
			require.Equal(t, "null", value.Get("spend_limit_configured").Raw)
			require.Equal(t, int64(404), value.Get("source_errors.0.status_code").Int())
		})
	}
}

func TestLimitsReviewedUnconfirmedSetRechecksBeforeWriting(t *testing.T) {
	for _, test := range []struct {
		name           string
		status         int
		project        bool
		projectMissing bool
		write          bool
	}{
		{"organization still unconfirmed", 404, false, false, true},
		{"project still unconfirmed", 404, true, false, true},
		{"new configuration", 200, false, false, false},
		{"access changed", 403, false, false, false},
		{"project disappeared", 404, true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var writes atomic.Int32
			project, scope := "", "organization"
			if test.project {
				project, scope = "proj_demo", "project"
			}
			backend := limitsTestBackend(t, project, 100, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/proj_demo") {
					if test.projectMissing {
						w.WriteHeader(http.StatusNotFound)
						limitsTestWrite(t, w, `{"error":{"message":"missing"}}`)
						return
					}
					limitsTestWrite(t, w, `{"id":"proj_demo","object":"organization.project"}`)
					return
				}
				if r.Method == http.MethodGet {
					w.WriteHeader(test.status)
					if test.status == 200 {
						limitsTestWrite(t, w, limitsTestLimit)
					} else {
						limitsTestWrite(t, w, `{"error":{"code":"not_found","message":"unconfirmed"}}`)
					}
					return
				}
				writes.Add(1)
				limitsTestWrite(t, w, `{"object":"`+scope+`.spend_limit","threshold_amount":12345,"currency":"USD","interval":"month"}`)
			})
			_, err := backend.Mutate(t.Context(), limitsChange{Kind: "limit_set", Amount: 12345, BeforeMissing: true})
			if test.write {
				require.NoError(t, err)
				require.Equal(t, int32(1), writes.Load())
			} else {
				require.Error(t, err)
				var failure *limitsMutationFailure
				require.ErrorAs(t, err, &failure)
				require.False(t, failure.Attempted)
				require.Zero(t, writes.Load())
			}
		})
	}
}

func TestLimitsMismatchedMutationKeepsReturnedRecordAndUncertainty(t *testing.T) {
	var writes atomic.Int32
	response := strings.TrimSuffix(limitsTestLimit, "}") + `,"future":{"exact":123456789012345678901234567890}}`
	backend := limitsTestBackend(t, "", 100, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			limitsTestWrite(t, w, limitsTestLimit)
			return
		}
		writes.Add(1)
		limitsTestWrite(t, w, response)
	})
	value, err := backend.Mutate(t.Context(), limitsChange{Kind: "limit_set", Amount: 12345, Before: gjson.Parse(limitsTestLimit)})
	require.ErrorContains(t, err, "different values")
	var failure *limitsMutationFailure
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Attempted)
	require.Equal(t, int32(1), writes.Load())
	require.Equal(t, response, value.Raw)
}

func TestLimitsCostsCancellationRetainsEarlierBuckets(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	started := make(chan struct{})
	backend := limitsTestCostsBackend(t, "", -1, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			bucket := limitsTestCostBucket(start, limitsTestCostResult("0.1", "usd", ""))
			limitsTestWrite(t, w, `{"object":"page","data":[`+bucket+`],"has_more":true,"next_page":"next_demo"}`)
			return
		}
		close(started)
		<-r.Context().Done()
	})
	type outcome struct {
		snapshot limitsSnapshot
		err      error
	}
	finished := make(chan outcome, 1)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { snapshot, err := backend.Load(ctx); finished <- outcome{snapshot, err} }()
	<-started
	cancel()
	result := <-finished
	require.ErrorIs(t, result.err, context.Canceled)
	require.Len(t, result.snapshot.Costs, 1)
	require.False(t, result.snapshot.CostsComplete)
	require.Empty(t, result.snapshot.CostsTotal)
	value, err := result.snapshot.JSON()
	require.NoError(t, err)
	require.Equal(t, "canceled", value.Get("source_errors.0.status").Str)
	require.Equal(t, "0.1", value.Get("costs.buckets.0.results.0.amount.value").Raw)
}

func TestLimitsServiceRejectsUneditableRatesWithoutRequests(t *testing.T) {
	for _, test := range []struct {
		name, baseline, field string
		value                 int64
	}{
		{"negative", limitsTestRate, "max_requests_per_1_minute", -1},
		{"absent", limitsTestRate, "max_tokens_per_1_day", 1},
		{"null", strings.Replace(limitsTestRate, `200`, `null`, 1), "max_tokens_per_1_minute", 1},
		{"fine tuned", strings.Replace(limitsTestRate, `synthetic-model`, `ft:synthetic-model`, 1), "max_requests_per_1_minute", 1},
		{"shared", strings.Replace(limitsTestRate, `synthetic-model`, `synthetic-shared`, 1), "max_requests_per_1_minute", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			backend := limitsTestBackend(t, "proj_demo", 100, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
			_, err := backend.Mutate(t.Context(), limitsChange{Kind: "rate_update", ID: "rate_demo", Before: gjson.Parse(test.baseline), RateValues: map[string]int64{test.field: test.value}})
			var failure *limitsMutationFailure
			require.ErrorAs(t, err, &failure)
			require.False(t, failure.Attempted)
			require.Zero(t, requests.Load())
		})
	}
}

func TestLimitsServiceSparseTokensPerDayConfirmation(t *testing.T) {
	const baseline = `{"object":"project.rate_limit","id":"rate_demo","model":"synthetic-model","max_tokens_per_1_day":100,"max_tokens_per_1_minute":null,"future":{"precision":0.1234567890123456789}}`
	const updated = `{"object":"project.rate_limit","id":"rate_demo","model":"synthetic-model","max_tokens_per_1_day":9007199254740993,"max_tokens_per_1_minute":null}`
	for _, test := range []struct {
		name, response string
		success        bool
	}{
		{"matching", updated, true},
		{"different model", strings.Replace(updated, `synthetic-model`, `other-model`, 1), false},
		{"rounded value", strings.Replace(updated, `9007199254740993`, `9007199254740992`, 1), false},
		{"missing value", strings.Replace(updated, `9007199254740993`, `null`, 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var writes atomic.Int32
			backend := limitsTestBackend(t, "proj_demo", 100, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/proj_demo") {
					limitsTestWrite(t, w, `{"object":"organization.project","id":"proj_demo"}`)
				} else if r.Method == http.MethodGet {
					limitsTestWrite(t, w, `{"object":"list","data":[`+baseline+`],"has_more":false}`)
				} else {
					writes.Add(1)
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					require.JSONEq(t, `{"max_tokens_per_1_day":9007199254740993}`, string(body))
					limitsTestWrite(t, w, test.response)
				}
			})
			_, err := backend.Mutate(t.Context(), limitsChange{Kind: "rate_update", ID: "rate_demo", Before: gjson.Parse(baseline), RateValues: map[string]int64{"max_tokens_per_1_day": 9007199254740993}})
			require.Equal(t, int32(1), writes.Load())
			if test.success {
				require.NoError(t, err)
			} else {
				var failure *limitsMutationFailure
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.Attempted)
			}
		})
	}
}
