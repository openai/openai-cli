package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/webhooks"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestWebhookCreateAPIFailureKeepsOtherStages(t *testing.T) {
	apierr := &openai.Error{StatusCode: http.StatusServiceUnavailable}
	for _, failure := range []error{
		nil,
		&openai.Error{StatusCode: http.StatusUnauthorized},
		&openai.Error{StatusCode: http.StatusBadRequest},
		&openai.Error{StatusCode: http.StatusTooManyRequests},
		&WorkflowError{"Could not load webhook event types.\nNo endpoint was created.", apierr},
		errors.New("synthetic local validation failure"),
		context.Canceled,
		errors.Join(apierr, context.Canceled),
	} {
		require.Equal(t, failure, webhookCreateRequestError(context.Background(), nil, failure))
	}
	for _, operation := range []string{"create", "update", "test", "event-types"} {
		t.Run(operation, func(t *testing.T) {
			app := webhookCreateTestCommand(webhookActionContext(operation, func(context.Context, *cli.Command) error { return apierr }, webhookTestCommandOptions()))
			failure := app.Run(t.Context(), []string{"openai", "webhooks", "create"})
			require.ErrorIs(t, failure, apierr)
			var workflow *WorkflowError
			require.Equal(t, operation == "create", errors.As(failure, &workflow))
		})
	}
}

func webhookCreateTestCommand(action cli.ActionFunc) *cli.Command {
	create := &cli.Command{Name: "create", Action: func(ctx context.Context, command *cli.Command) error {
		state, _ := ctx.Value(webhookCommandKey{}).(webhookCommandContext)
		state.options = webhookTestCommandOptions()
		return action(context.WithValue(ctx, webhookCommandKey{}, state), command)
	}, Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "name", BodyPath: "name", Required: true},
		&requestflag.Flag[string]{Name: "url", BodyPath: "url", Required: true},
		&requestflag.Flag[[]string]{Name: "event-type", BodyPath: "event_types", Required: true},
	}}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "format-error"},
			&cli.StringFlag{Name: "transform"}, &cli.StringFlag{Name: "transform-error"},
			&cli.BoolFlag{Name: "raw-output"}, &cli.BoolFlag{Name: "quiet"}, &cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "base-url"}, request.NewRequestHeaderFlag(),
			&requestflag.Flag[string]{Name: "api-key"}, &requestflag.Flag[string]{Name: "admin-api-key"},
			&requestflag.Flag[string]{Name: "organization"}, &requestflag.Flag[string]{Name: "project"},
			&requestflag.Flag[string]{Name: "webhook-secret"},
		}, Commands: []*cli.Command{{Name: "webhooks", Commands: []*cli.Command{create}}},
	}
	request.ConfigureRootFlags(root)
	return root
}

func TestWebhookCreateRequestedKeepsExplicitModes(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"bare", nil, true},
		{"name", []string{"--name", "example"}, false},
		{"empty name", []string{"--name="}, false},
		{"event", []string{"--event-type", "response.completed"}, false},
		{"url", []string{"--url", "https://example.invalid/hook"}, false},
		{"argument", []string{"extra"}, false},
		{"json", []string{"--format", "json"}, false},
		{"text", []string{"--format", "text"}, false},
		{"auto", []string{"--format", "auto"}, false},
		{"error json", []string{"--format-error", "json"}, false},
		{"extraction", []string{"--transform", "id"}, false},
		{"empty extraction", []string{"--transform="}, false},
		{"error extraction", []string{"--transform-error", "message"}, false},
		{"raw", []string{"--raw-output"}, false},
		{"explicit false raw", []string{"--raw-output=false"}, false},
		{"quiet", []string{"--quiet"}, false},
		{"debug", []string{"--debug"}, false},
		{"project", []string{"--project", "proj_synthetic"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			app := webhookCreateTestCommand(func(_ context.Context, command *cli.Command) error {
				called = true
				require.Equal(t, test.want, webhookCreateRequested(command, func(string) string { return "" }))
				for _, env := range []map[string]string{{"CI": "true"}, {"TERM": "dumb"}, {"TERM": "DUMB"}} {
					require.False(t, webhookCreateRequested(command, func(key string) string { return env[key] }))
				}
				return nil
			})
			require.NoError(t, app.Run(t.Context(), append([]string{"openai", "webhooks", "create"}, test.args...)))
			require.True(t, called)
		})
	}
}

func TestWebhookCreateNonterminalDelegates(t *testing.T) {
	want := errors.New("generated behavior retained")
	called := 0
	app := webhookCreateTestCommand(webhookCreateWorkflow(func(context.Context, *cli.Command) error { called++; return want }))
	require.ErrorIs(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}), want)
	require.Equal(t, 1, called)
}

func TestWebhookCreateCatalogPreservesExactNames(t *testing.T) {
	events, err := parseWebhookCreateEvents([]byte(`{"object":"list","data":["response.completed","future.event","response.completed","@literal","\\@literal","escape\u001b[31m.event"],"future":true}`))
	require.NoError(t, err)
	require.Equal(t, []string{"response.completed", "future.event", "@literal", `\@literal`, "escape\x1b[31m.event"}, events)
	for _, raw := range []string{`null`, `[]`, `{}`, `{"object":"list"}`, `{"object":"other","data":["x"]}`,
		`{"object":"list","data":null}`, `{"object":"list","data":[]}`, `{"object":"list","data":[""]}`,
		`{"object":"list","data":[" "]}`, `{"object":"list","data":[4]}`, `{"object":"list","data":["x",null]}`,
		`{"object":"list","data":["x"]} trailing`,
		`{"object":"list","object":"list","data":["x"]}`,
		`{"object":"list","data":["x"],"data":["y"]}`,
		`{"object":"list","data":["x"],"\u0064ata":["y"]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseWebhookCreateEvents([]byte(raw))
			require.Error(t, err)
			require.Nil(t, got)
			var workflow *WorkflowError
			require.ErrorAs(t, err, &workflow)
			require.Contains(t, workflow.Message, "No endpoint was created")
			require.Contains(t, workflow.Message, "webhooks event-types list --format json")
			require.Contains(t, workflow.Message, "webhooks create --help")
		})
	}
}

func TestWebhookCreateCatalogKeepsUnknownOrdinaryFields(t *testing.T) {
	events, err := parseWebhookCreateEvents([]byte(`{"object":"list","data":["new.event"],"future":{"object":"a","data":"b"},"another":[1,2]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"new.event"}, events)
}

func TestWebhookCreateDiscoveryUsesProjectAndHeaderOptions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-webhook-synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/webhook_event_types", r.URL.Path)
		require.Equal(t, "proj_synthetic", r.Header.Get("OpenAI-Project"))
		require.Equal(t, "org_synthetic", r.Header.Get("OpenAI-Organization"))
		require.Equal(t, "yes", r.Header.Get("X-Synthetic-Header"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":["response.completed","future.event"]}`)
	}))
	defer server.Close()
	app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
		events, err := discoverWebhookCreateEvents(ctx, command)
		require.NoError(t, err)
		require.Equal(t, []string{"response.completed", "future.event"}, events)
		require.False(t, command.IsSet("name"))
		require.False(t, command.IsSet("event-type"))
		return err
	})
	require.NoError(t, app.Run(t.Context(), []string{"openai", "--base-url", server.URL, "--project", "proj_synthetic",
		"--organization", "org_synthetic", "--header", "X-Synthetic-Header: yes", "webhooks", "create"}))
	require.Equal(t, 1, requests)
}

func TestWebhookCreateDiscoveryErrorsKeepSafeGuidanceAndAPIErrors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-webhook-synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status == http.StatusUnauthorized {
				_, _ = io.WriteString(w, `{"error":{"message":"synthetic authentication failure","type":"invalid_request_error"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"object":"list","data":42,"private":"synthetic-private-body"}`)
		}))
		app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
			events, err := discoverWebhookCreateEvents(ctx, command)
			require.Error(t, err)
			require.Nil(t, events)
			var workflow *WorkflowError
			if status == http.StatusUnauthorized {
				var apiError *openai.Error
				require.ErrorAs(t, err, &apiError)
				require.ErrorAs(t, err, &workflow)
				require.Contains(t, err.Error(), "No endpoint was created")
				require.Equal(t, status, apiError.StatusCode)
			} else {
				require.ErrorAs(t, err, &workflow)
				require.Contains(t, err.Error(), "No endpoint was created")
				require.Contains(t, err.Error(), "webhooks event-types list --format json")
				require.NotContains(t, err.Error(), "synthetic-private-body")
			}
			return nil
		})
		err := app.Run(t.Context(), []string{"openai", "--base-url", server.URL, "webhooks", "create"})
		server.Close()
		require.NoError(t, err)
	}
}

func TestWebhookCreateFlowNeverSubmitsWithoutConfirmation(t *testing.T) {
	for _, mode := range []string{"discovery error", "canceled before", "canceled during discovery", "picker error", "no", "canceled during picker", "missing events", "unknown event", "yes", "action error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "canceled before" {
				cancel()
			}
			failure := errors.New("synthetic failure")
			discoveries, picks, creates := 0, 0, 0
			app := webhookCreateTestCommand(func(_ context.Context, command *cli.Command) error {
				return runWebhookCreateFlow(ctx, command, func(context.Context, *cli.Command) error {
					creates++
					if mode == "action error" {
						return failure
					}
					return nil
				}, func(context.Context, *cli.Command) ([]string, error) {
					discoveries++
					if mode == "discovery error" {
						return nil, failure
					}
					if mode == "canceled during discovery" {
						cancel()
					}
					return []string{"response.completed"}, nil
				}, func(context.Context, []string) (webhookCreateSettings, bool, error) {
					picks++
					if mode == "picker error" {
						return webhookCreateSettings{}, false, failure
					}
					settings := webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}}
					if mode == "canceled during picker" {
						cancel()
					}
					if mode == "missing events" {
						settings.events = nil
					}
					if mode == "unknown event" {
						settings.events = []string{"not.returned"}
					}
					return settings, mode != "no", nil
				})
			})
			err := app.Run(t.Context(), []string{"openai", "webhooks", "create"})
			if mode == "yes" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if mode == "yes" || mode == "action error" {
				require.Equal(t, 1, creates)
			} else {
				require.Zero(t, creates)
			}
			if mode == "no" {
				var exit cli.ExitCoder
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 130, exit.ExitCode())
			}
			if mode == "canceled before" {
				require.Zero(t, discoveries)
			}
			if mode == "canceled before" || mode == "canceled during discovery" || mode == "discovery error" {
				require.Zero(t, picks)
			}
			if mode == "canceled before" || mode == "canceled during discovery" || mode == "canceled during picker" {
				var submitted *CreateCanceledError
				require.False(t, errors.As(err, &submitted), "pre-submit cancellation must retain its ordinary behavior")
			}
		})
	}
}

func TestWebhookCreateLiteralBodyAndFlagRestoration(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-webhook-synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	for _, name := range []string{"@literal", `\@literal`, "@data://-", "synthetic name", "日本語"} {
		t.Run(name, func(t *testing.T) {
			settings := webhookCreateSettings{name, "https://example.invalid/hook?x=synthetic", []string{"@literal", `\@literal`, "future.event"}}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "/webhook_endpoints", r.URL.Path)
				var body struct {
					Name   string   `json:"name"`
					URL    string   `json:"url"`
					Events []string `json:"event_types"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, settings.name, body.Name)
				require.Equal(t, settings.url, body.URL)
				require.Equal(t, settings.events, body.Events)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"webhook_endpoint","id":"whe_synthetic"}`)
			}))
			defer server.Close()
			app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
				original := slices.Clone(command.Flags)
				before := requestflag.ExtractRequestContents(command)
				failure := errors.New("synthetic output failure after create")
				for i := range 2 {
					err := runWebhookCreateAction(ctx, command, settings, func(ctx context.Context, command *cli.Command) error {
						options, err := request.FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, request.ApplicationJSON, true, nil, request.PreparationHooks{})
						require.NoError(t, err)
						client := openai.NewClient(request.DefaultOptions(command, "test")...)
						_, err = client.Webhooks.New(ctx, webhooks.WebhookNewParams{}, options...)
						require.NoError(t, err)
						if i == 1 {
							return failure
						}
						return err
					})
					if i == 1 {
						require.ErrorIs(t, err, failure)
					} else {
						require.NoError(t, err)
					}
					for index, flag := range original {
						require.Same(t, flag, command.Flags[index])
					}
					require.Equal(t, before, requestflag.ExtractRequestContents(command))
					require.Equal(t, []string{"original.event"}, command.Value("event-type"))
					require.Equal(t, "original", command.String("name"))
					require.False(t, command.IsSet("url"))
				}
				return nil
			})
			require.NoError(t, app.Run(t.Context(), []string{"openai", "--base-url", server.URL,
				"webhooks", "create", "--name", "original", "--event-type", "original.event"}))
			require.Equal(t, 2, requests)
		})
	}
}

func TestWebhookCreatePreparationFailurePreservesFlags(t *testing.T) {
	want := errors.New("synthetic event validation failed")
	app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
		original := slices.Clone(command.Flags)
		before := requestflag.ExtractRequestContents(command)
		for _, flag := range command.Flags {
			if events, ok := flag.(*requestflag.Flag[[]string]); ok {
				events.Validator = func(values []string) error {
					require.Equal(t, []string{"response.completed"}, values)
					return want
				}
			}
		}
		err := runWebhookCreateAction(ctx, command,
			webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}},
			func(context.Context, *cli.Command) error { t.Fatal("validation must prevent dispatch"); return nil })
		require.ErrorIs(t, err, want)
		require.Equal(t, before, requestflag.ExtractRequestContents(command))
		for index, flag := range original {
			require.Same(t, flag, command.Flags[index])
		}
		return nil
	})
	require.NoError(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}))
}

func TestWebhookCreateDispatchFailureRetainsCauseWithoutRetry(t *testing.T) {
	apiFailure := &openai.Error{StatusCode: http.StatusBadRequest}
	for _, failure := range []error{apiFailure, context.Canceled, io.ErrClosedPipe,
		&output.WriteError{Err: io.ErrClosedPipe}, errors.New("sensitive URL or response body"),
	} {
		app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
			calls := 0
			err := runWebhookCreateAction(ctx, command,
				webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}},
				func(context.Context, *cli.Command) error { calls++; return failure })
			require.Equal(t, 1, calls, "the UI must not retry after an uncertain result")
			require.ErrorIs(t, err, failure)
			require.Equal(t, output.IsBrokenPipe(failure), output.IsBrokenPipe(err))
			if failure == apiFailure {
				require.Same(t, failure, err)
			} else if failure != context.Canceled {
				var workflow *WorkflowError
				require.ErrorAs(t, err, &workflow)
				require.Contains(t, err.Error(), "endpoint may already exist")
				require.Contains(t, err.Error(), "webhooks list with your original authentication, project, organization, and API settings before repeating create")
				require.NotContains(t, err.Error(), "sensitive")
			}
			return nil
		})
		require.NoError(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}))
	}
}

func TestWebhookCreateCanceledDispatchKeepsCauseAndSafeRecovery(t *testing.T) {
	cause := errors.Join(context.Canceled, io.ErrClosedPipe, errors.New("synthetic-private-response-and-url"))
	app := webhookCreateTestCommand(func(ctx context.Context, command *cli.Command) error {
		calls := 0
		err := runWebhookCreateAction(ctx, command,
			webhookCreateSettings{"Webhook", "https://example.invalid/hook", []string{"response.completed"}},
			func(context.Context, *cli.Command) error { calls++; return cause })
		require.Equal(t, 1, calls)
		var submitted *CreateCanceledError
		require.ErrorAs(t, err, &submitted)
		require.Same(t, cause, submitted.Unwrap())
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, io.ErrClosedPipe)
		require.Contains(t, err.Error(), "Request canceled. The create request may have reached the API.")
		require.Contains(t, err.Error(), "original authentication, project, organization, and API settings")
		require.NotContains(t, err.Error(), "synthetic-private")
		require.False(t, output.IsBrokenPipe(err))
		return nil
	})
	require.NoError(t, app.Run(t.Context(), []string{"openai", "webhooks", "create"}))
}

// Keep test request preparation local; application policy has separate coverage.
func webhookTestCommandOptions() CommandOptions {
	return CommandOptions{
		PrepareDiscovery: func(command *cli.Command) ([]option.RequestOption, error) {
			return request.FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets,
				request.EmptyBody, true, nil, request.PreparationHooks{})
		},
		DefaultRequestOptions: func(command *cli.Command) []option.RequestOption { return request.DefaultOptions(command, "test") },
	}
}

func TestWebhookCreateTransportGuidanceUsesInvocationPolicy(t *testing.T) {
	failure := &url.Error{Op: "Post", URL: "https://example.invalid/?token=synthetic-private", Err: io.EOF}
	for _, allowed := range []bool{false, true} {
		callbackCalls := 0
		ctx := context.WithValue(t.Context(), webhookCommandKey{}, webhookCommandContext{options: CommandOptions{
			ReadableTransportFailure: func(command *cli.Command, err error) (string, bool) {
				callbackCalls++
				require.Same(t, failure, err)
				return "The request failed.", allowed
			},
		}})
		result := webhookCreateRequestError(ctx, nil, failure)
		require.ErrorIs(t, result, failure)
		require.Equal(t, 1, callbackCalls)
		if !allowed {
			require.Same(t, failure, result)
		} else {
			require.True(t, strings.HasPrefix(result.Error(), "The request failed.\nWebhook creation is unconfirmed."))
			require.NotContains(t, result.Error(), "synthetic-private")
		}
	}
	require.Same(t, failure, webhookCreateRequestError(t.Context(), nil, failure), "missing policy must preserve the original error")
	for _, unchanged := range []error{
		&url.Error{Op: "Get", Err: io.EOF},
		&url.Error{Op: "Post", Err: context.Canceled},
		errors.New("synthetic local validation error"),
	} {
		ctx := context.WithValue(t.Context(), webhookCommandKey{}, webhookCommandContext{options: CommandOptions{
			ReadableTransportFailure: func(*cli.Command, error) (string, bool) {
				t.Fatal("policy must only receive uncertain Post failures")
				return "", false
			},
		}})
		require.Same(t, unchanged, webhookCreateRequestError(ctx, nil, unchanged))
	}
}
