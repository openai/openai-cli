package custom

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const agentsRetryEvent = `{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"synthetic input"}]}]}`

type agentsRetryRequest struct {
	key, body, method, path, beta string
	organization, project         string
	keyCount                      int
}

type agentsRetryInput struct {
	args               []string
	environmentHeaders string
	status, failures   int
	disconnect         bool
	stdin              string
	resource           string
	colonRoute         bool
	maxRetries         *int
	disableServerRetry bool
}

func runAgentsEventRetry(t *testing.T, args []string, environmentHeaders string, status int) ([]agentsRetryRequest, error) {
	return runAgentsRetryRequest(t, agentsRetryInput{args: args, environmentHeaders: environmentHeaders, status: status})
}

func runAgentsRetryRequest(t *testing.T, input agentsRetryInput) ([]agentsRetryRequest, error) {
	t.Helper()
	if input.status == 0 {
		input.status = http.StatusServiceUnavailable
	}
	if input.failures == 0 {
		input.failures = 1
	}
	if input.resource == "" {
		input.resource = "beta:agents:sessions:events"
	}
	var requests []agentsRetryRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("could not read synthetic request body")
			return
		}
		mu.Lock()
		requests = append(requests, agentsRetryRequest{
			key: r.Header.Get("Idempotency-Key"), body: string(body), method: r.Method, path: r.URL.Path,
			beta: r.Header.Get("OpenAI-Beta"), organization: r.Header.Get("OpenAI-Organization"),
			project: r.Header.Get("OpenAI-Project"), keyCount: len(r.Header.Values("Idempotency-Key")),
		})
		attempt := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After-Ms", "1")
		w.Header().Set("X-Request-ID", fmt.Sprintf("req_synthetic_%d", attempt))
		if input.disableServerRetry {
			w.Header().Set("X-Should-Retry", "false")
		}
		if attempt <= input.failures {
			if input.disconnect {
				connection, _, hijackErr := w.(http.Hijacker).Hijack()
				if hijackErr != nil {
					t.Error("could not close synthetic connection")
					return
				}
				if connection.Close() != nil {
					t.Error("could not close synthetic connection")
				}
				return
			}
			w.WriteHeader(input.status)
			fmt.Fprint(w, `{"error":{"message":"synthetic retryable failure","type":"server_error"}}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "sk-synthetic-agents-retry")
	t.Setenv("OPENAI_BASE_URL", server.URL+"/")
	t.Setenv("OPENAI_ORG_ID", "org_synthetic")
	t.Setenv("OPENAI_PROJECT_ID", "proj_synthetic")
	t.Setenv("OPENAI_CUSTOM_HEADERS", input.environmentHeaders)
	t.Setenv("OPENAI_UNTRUSTED_STDIN", "false")
	if input.stdin != "" {
		file, err := os.CreateTemp(t.TempDir(), "input-*.json")
		require.NoError(t, err)
		_, err = file.WriteString(input.stdin)
		require.NoError(t, err)
		_, err = file.Seek(0, io.SeekStart)
		require.NoError(t, err)
		previous := os.Stdin
		os.Stdin = file
		t.Cleanup(func() { os.Stdin = previous; require.NoError(t, file.Close()) })
	}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags:          []cli.Flag{NewRequestHeaderFlag()},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{{Name: input.resource, Category: "API RESOURCE", Commands: []*cli.Command{{
			Name: "create", Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "session-id", Required: true, PathParam: "session_id"},
				&requestflag.Flag[[]map[string]any]{Name: "event", Required: true, BodyPath: "events"},
				&requestflag.Flag[string]{Name: "idempotency-key", HeaderPath: "Idempotency-Key"},
			}, Action: func(ctx context.Context, command *cli.Command) error {
				// Match the generated handler's order: construct the client before
				// parsing request data, then supply its header/body request options.
				options := append(GetDefaultRequestOptions(command), option.WithMaxRetryDelay(time.Millisecond))
				if input.maxRetries != nil {
					options = append(options, option.WithMaxRetries(*input.maxRetries))
				}
				client := openai.NewClient(options...)
				requestOptions, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, input.stdin == "")
				if err != nil {
					return err
				}
				switch input.resource {
				case "beta:agents":
					_, err = client.Beta.Agents.New(ctx, openai.BetaAgentNewParams{}, requestOptions...)
				case "beta:agents:sessions":
					_, err = client.Beta.Agents.Sessions.New(ctx, openai.BetaAgentSessionNewParams{}, requestOptions...)
				default:
					err = client.Beta.Agents.Sessions.Events.New(ctx, command.String("session-id"), openai.BetaAgentSessionEventNewParams{}, requestOptions...)
				}
				return err
			},
		}}}},
	}
	configureCommandSubgroups(root)
	path := strings.Split(input.resource, ":")
	if input.colonRoute {
		path = []string{input.resource}
	}
	command := append([]string{"openai"}, path...)
	command = append(command, "create", "--session-id", "sess_synthetic")
	if input.stdin == "" {
		command = append(command, "--event", agentsRetryEvent)
	}
	command = append(command, input.args...)
	err := root.Run(context.Background(), command)
	mu.Lock()
	defer mu.Unlock()
	return append([]agentsRetryRequest(nil), requests...), err
}

func TestAgentsEventRetryUsesEffectiveWireKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key.txt")
	emptyFile := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(keyFile, []byte("key_file"), 0600))
	require.NoError(t, os.WriteFile(emptyFile, nil, 0600))
	for _, test := range []struct {
		name, environment, key string
		args                   []string
		retry                  bool
	}{
		{"absent", "", "", nil, false},
		{"empty flag", "", "", []string{"--idempotency-key", ""}, false},
		{"wire whitespace", "", "", []string{"--idempotency-key", " \t "}, false},
		{"trimmed wire key", "", "key_trimmed", []string{"--idempotency-key", " \tkey_trimmed\t "}, true},
		{"unicode", "", "clé", []string{"--idempotency-key", "clé"}, false},
		{"maximum", "", strings.Repeat("k", 256), []string{"--idempotency-key", strings.Repeat("k", 256)}, true},
		{"over maximum", "", strings.Repeat("k", 257), []string{"--idempotency-key", strings.Repeat("k", 257)}, false},
		{"literal null", "", "null", []string{"--idempotency-key", "null"}, true},
		{"environment case last wins", "Idempotency-Key: first\nidempotency-key: key_env", "key_env", nil, true},
		{"flag replaces environment", "Idempotency-Key: key_env", "key_flag", []string{"--idempotency-key", "key_flag"}, true},
		{"empty flag replaces environment", "Idempotency-Key: key_env", "", []string{"--idempotency-key", ""}, false},
		{"root replaces flag", "Idempotency-Key: key_env", "key_root", []string{"--idempotency-key", "key_flag", "--header", "idempotency-key: key_root"}, true},
		{"root before flag still overrides", "", "key_root", []string{"--header", "Idempotency-Key: key_root", "--idempotency-key", "key_flag"}, true},
		{"empty root replaces flag", "", "", []string{"--idempotency-key", "key_flag", "--header", "IDEMPOTENCY-KEY:"}, false},
		{"header alias last wins", "", "last", []string{"--header", "Idempotency-Key: first", "-H", "idempotency-key: last"}, true},
		{"last root empty", "", "", []string{"-H", "Idempotency-Key: first", "--header", "idempotency-key: \t"}, false},
		{"last endpoint flag wins", "", "last", []string{"--idempotency-key", "first", "--idempotency-key", "last"}, true},
		{"key file", "", "key_file", []string{"--idempotency-key", "@" + keyFile}, true},
		{"empty key file replaces environment", "Idempotency-Key: key_env", "", []string{"--idempotency-key", "@" + emptyFile}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests, err := runAgentsEventRetry(t, test.args, test.environment, http.StatusServiceUnavailable)
			if test.retry {
				require.NoError(t, err)
				require.Len(t, requests, 2)
				require.Equal(t, requests[0], requests[1])
			} else {
				require.Error(t, err)
				require.Len(t, requests, 1)
			}
			require.Equal(t, test.key, requests[0].key)
			require.LessOrEqual(t, requests[0].keyCount, 1, "repeated options must become one final wire header")
		})
	}
}

func TestAgentsEventRetryUsesFinalOverridesAndYAML(t *testing.T) {
	messageBody := `{"events":[` + agentsRetryEvent + `],"Idempotency-Key":"key_stdin"}`
	cancelBody := `{"events":[{"type":"agent.session.input.cancel"}],"Idempotency-Key":"key_stdin"}`
	for _, test := range []struct {
		name, body, key string
		args            []string
		retry           bool
	}{
		{"YAML key and body", "Idempotency-Key: key_yaml\nevents:\n  - type: agent.session.input.message\n    input:\n      - role: user\n        content:\n          - type: input_text\n            text: synthetic\n", "key_yaml", nil, true},
		{"final CLI message replaces cancellation", cancelBody, "key_stdin", []string{"--event", agentsRetryEvent}, true},
		{"final CLI cancellation replaces message", messageBody, "key_stdin", []string{"--event", `{"type":"agent.session.input.cancel"}`}, false},
		{"empty explicit key beats stdin", messageBody, "", []string{"--idempotency-key", ""}, false},
		{"root key beats stdin", messageBody, "key_root", []string{"-H", "iDeMpOtEnCy-KeY: key_root"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests, err := runAgentsRetryRequest(t, agentsRetryInput{stdin: test.body, args: test.args})
			if test.retry {
				require.NoError(t, err)
				require.Len(t, requests, 2)
				require.Equal(t, requests[0], requests[1])
			} else {
				require.Error(t, err)
				require.Len(t, requests, 1)
			}
			require.Equal(t, test.key, requests[0].key)
		})
	}
}

func TestAgentsEventRetryRetainsSDKAndServerControls(t *testing.T) {
	zero := 0
	for _, input := range []agentsRetryInput{
		{args: []string{"--idempotency-key", "key_no_retry"}, maxRetries: &zero},
		{args: []string{"--idempotency-key", "key_no_retry"}, disableServerRetry: true},
		{status: http.StatusTooManyRequests},
		{disconnect: true},
		{disconnect: true, stdin: `{"events":[` + agentsRetryEvent + `,{"type":"agent.session.input.cancel"}],"Idempotency-Key":"key_mixed"}`},
	} {
		requests, err := runAgentsRetryRequest(t, input)
		require.Error(t, err)
		require.Len(t, requests, 1)
	}
}

func TestAgentsMessageOnlyBodyRejectsAmbiguousRawKeys(t *testing.T) {
	for _, body := range []string{
		`{"events":[` + agentsRetryEvent + `],"events":[{"type":"agent.session.input.cancel"}]}`,
		`{"events":[` + agentsRetryEvent + `],"ev\u0065nts":[` + agentsRetryEvent + `]}`,
		`{"events":[{"type":"agent.session.input.message","type":"agent.session.input.cancel"}]}`,
		`{"events":[{"type":"agent.session.input.message","\u0074ype":"agent.session.input.message"}]}`,
		`{"events":[` + agentsRetryEvent + `,{}]}`,
		`{"events":[{"type":null}]}`,
		`{"events":[{"type":123}]}`,
		`{"events":[` + agentsRetryEvent,
		`{"events":{"#":1}}`,
	} {
		require.False(t, agentsMessageOnlyBody([]byte(body)), body)
	}
	large := `{"events":[` + strings.Replace(agentsRetryEvent, "synthetic input", strings.Repeat("x", 1<<20), 1) + `]}`
	require.True(t, agentsMessageOnlyBody([]byte(large)))
}

func TestAgentsEventRetryInvalidHeaderNameCannotSupplyKey(t *testing.T) {
	command := &cli.Command{Name: "create", Metadata: map[string]any{resourceCommandMetadata: "beta:agents:sessions:events"}}
	body := []byte(`{"events":[` + agentsRetryEvent + `]}`)
	t.Setenv("OPENAI_CUSTOM_HEADERS", "Idempotency-Key: key_invalid_name")
	require.NotEmpty(t, agentsResolvedRequestOptions(command, nil, nil, body), "Unicode case folding must not establish an HTTP header")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	require.NotEmpty(t, agentsResolvedRequestOptions(command, map[string]any{"Idempotency-Key": "key_invalid_name"}, nil, body))
	require.NotEmpty(t, agentsResolvedRequestOptions(command, nil, http.Header{"Idempotency-Key": {"first", "second"}}, body), "duplicate wire values cannot establish the supported key")
}

func TestAgentsEventRetryRequiresMessageOnlyFinalBody(t *testing.T) {
	for _, test := range []struct {
		name, events string
		retry        bool
	}{
		{"one message", "[" + agentsRetryEvent + "]", true},
		{"two messages", "[" + agentsRetryEvent + "," + agentsRetryEvent + "]", true},
		{"empty", "[]", false},
		{"null", "null", false},
		{"object", agentsRetryEvent, false},
		{"cancel", `[{"type":"agent.session.input.cancel"}]`, false},
		{"tool", `[{"type":"agent.session.input.tool_result"}]`, false},
		{"browser approval", `[{"type":"agent.session.input.computer_use_approval_request_result"}]`, false},
		{"mixed", "[" + agentsRetryEvent + `,{"type":"agent.session.input.cancel"}]`, false},
		{"unknown", `[{"type":"future.event"}]`, false},
		{"scalar", `["agent.session.input.message"]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"events":` + test.events + `,"Idempotency-Key":"key_stdin"}`
			requests, err := runAgentsRetryRequest(t, agentsRetryInput{stdin: body})
			if test.retry {
				require.NoError(t, err)
				require.Len(t, requests, 2)
				require.Equal(t, requests[0], requests[1])
			} else {
				require.Error(t, err)
				require.Len(t, requests, 1)
			}
			require.Equal(t, "key_stdin", requests[0].key)
			require.NotContains(t, requests[0].body, "Idempotency-Key")
		})
	}
}

func TestAgentsEventRetryPreservesAliasesConnectionAndExhaustion(t *testing.T) {
	for _, input := range []agentsRetryInput{
		{colonRoute: true, args: []string{"-H", "Idempotency-Key: key_alias"}},
		{disconnect: true, args: []string{"--idempotency-key", "key_connection"}},
		{failures: 3, args: []string{"--idempotency-key", "key_exhausted"}},
	} {
		requests, err := runAgentsRetryRequest(t, input)
		if input.failures == 3 {
			var apiError *openai.Error
			require.ErrorAs(t, err, &apiError)
			require.Equal(t, "req_synthetic_3", apiError.Response.Header.Get("X-Request-ID"))
			require.Len(t, requests, 3)
		} else {
			require.NoError(t, err)
			require.Len(t, requests, 2)
		}
		for _, request := range requests[1:] {
			require.Equal(t, requests[0], request)
		}
	}
}

func TestAgentsEventRetryDoesNotChangeOtherCreationContracts(t *testing.T) {
	for _, resource := range []string{"beta:agents", "beta:agents:sessions"} {
		requests, err := runAgentsRetryRequest(t, agentsRetryInput{resource: resource, args: []string{"--idempotency-key", "key_unsupported"}})
		require.Error(t, err)
		require.Len(t, requests, 1)
	}
}

func TestAgentsEventRetryWithSupportedIdempotencyKey(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			requests, err := runAgentsEventRetry(t, []string{"--idempotency-key", "key_synthetic"}, "", status)
			require.NoError(t, err)
			require.Len(t, requests, 2)
			require.Equal(t, requests[0], requests[1], "retry must preserve the effective key and serialized event body")
			require.Equal(t, "key_synthetic", requests[0].key)
			require.Equal(t, http.MethodPost, requests[0].method)
			require.Equal(t, "/agents/sessions/sess_synthetic/events", requests[0].path)
			require.Equal(t, "agents=v1", requests[0].beta)
			require.Equal(t, "org_synthetic", requests[0].organization)
			require.Equal(t, "proj_synthetic", requests[0].project)
			require.True(t, strings.Contains(requests[0].body, "synthetic input"))
		})
	}
}
