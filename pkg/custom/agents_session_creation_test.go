package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestAgentsSessionCreationIntentUsesOnlyMissingOrNullInput(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{
		{"omitted", `{"environment":{"type":"self_hosted"}}`, true},
		{"null", `{"environment":{"type":"self_hosted"},"input":null}`, true},
		{"whitespace around null", `{"environment":{"type":"self_hosted"},"input": null }`, true},
		{"escaped enum", `{"environment":{"type":"self\u005fhosted"}}`, true},
		{"escaped canonical key", `{"environment":{"type":"self_hosted"},"\u0069nput":null}`, true},
		{"nested unrelated input", `{"environment":{"type":"self_hosted","env":{"input":"synthetic"}},"metadata":{"input":"synthetic"}}`, true},
		{"empty array", `{"environment":{"type":"self_hosted"},"input":[]}`, false},
		{"empty string", `{"environment":{"type":"self_hosted"},"input":""}`, false},
		{"literal null", `{"environment":{"type":"self_hosted"},"input":"null"}`, false},
		{"string whitespace", `{"environment":{"type":"self_hosted"},"input":" \n\t "}`, false},
		{"message", `{"environment":{"type":"self_hosted"},"input":"synthetic task"}`, false},
		{"false", `{"environment":{"type":"self_hosted"},"input":false}`, false},
		{"zero", `{"environment":{"type":"self_hosted"},"input":0}`, false},
		{"object", `{"environment":{"type":"self_hosted"},"input":{}}`, false},
		{"hosted", `{"environment":{"type":"openai_hosted"}}`, false},
		{"none", `{"environment":{"type":"none"}}`, false},
		{"missing environment", `{}`, false},
		{"null environment", `{"environment":null}`, false},
		{"string environment", `{"environment":"self_hosted"}`, false},
		{"null type", `{"environment":{"type":null}}`, false},
		{"unknown type", `{"environment":{"type":"future"}}`, false},
		{"many duplicate inputs", `{"environment":{"type":"self_hosted"},` + strings.Repeat(`"input":null,`, 256) + `"input":null}`, false},
		{"duplicate input", `{"environment":{"type":"self_hosted"},"input":null,"input":null}`, false},
		{"duplicate environment", `{"environment":{"type":"self_hosted"},"environment":{"type":"self_hosted"}}`, false},
		{"duplicate type", `{"environment":{"type":"self_hosted","type":"self_hosted"}}`, false},
		{"input case ambiguity", `{"environment":{"type":"self_hosted"},"Input":null}`, false},
		{"environment case ambiguity", `{"Environment":{"type":"self_hosted"}}`, false},
		{"type case ambiguity", `{"environment":{"Type":"self_hosted"}}`, false},
		{"conflicting case", `{"environment":{"type":"self_hosted"},"input":"synthetic","Input":null}`, false},
		{"malformed", `{"environment":{"type":"self_hosted"}`, false},
		{"trailing document", `{"environment":{"type":"self_hosted"}} {}`, false},
		{"root null", `null`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			require.Equal(t, tc.allowed, agentsSelfHostedCreationWithoutInput(body))
			require.Equal(t, tc.body, string(body), "classification must not alter request bytes")
		})
	}
}

func TestAgentsSessionCreationIntentSkipsLargeValues(t *testing.T) {
	payload := strings.Repeat("synthetic", 1024*1024)
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{
		{"large input", `{"environment":{"type":"self_hosted"},"input":"` + payload + `"}`, false},
		{"large ignored instructions", `{"environment":{"type":"self_hosted","env":{"SYNTHETIC":"` + payload + `"}},"agent":{"instructions":"` + payload + `"}}`, true},
		{"large unknown type", `{"environment":{"type":"` + payload + `"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			require.Equal(t, tc.allowed, agentsSelfHostedCreationWithoutInput(body))
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			allowed := agentsSelfHostedCreationWithoutInput(body)
			runtime.ReadMemStats(&after)
			require.Equal(t, tc.allowed, allowed)
			require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(128*1024), "intent parsing must not copy large input values")
		})
	}
}

func TestAgentsSessionCreationActionScopeAndContextIsolation(t *testing.T) {
	type parentKey struct{}
	var cells []*agentsSessionCreationIntent
	var calls []string
	action := func(ctx context.Context, command *cli.Command) error {
		require.Equal(t, "parent context", ctx.Value(parentKey{}))
		cell, _ := ctx.Value(agentsSessionCreationKey{}).(*agentsSessionCreationIntent)
		calls = append(calls, commandResourceName(command)+"/"+command.Name)
		if cell != nil {
			require.False(t, cell.withoutInput.Load(), "new actions must start strict")
			cell.withoutInput.Store(true)
			cells = append(cells, cell)
		}
		return nil
	}
	root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{
		{Name: "beta:agents:sessions", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "create", Action: action}, {Name: "retrieve", Action: action}}},
		{Name: "beta:agents:sessions:events", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "create", Action: action}}},
		{Name: "beta:agents", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "create", Action: action}}},
	}}
	configureAgentsSessionCreation(root)
	configureCommandSubgroups(root)
	ctx := context.WithValue(context.Background(), parentKey{}, "parent context")
	for _, args := range [][]string{
		{"openai", "beta", "agents", "sessions", "create"},
		{"openai", "beta:agents:sessions", "create"},
		{"openai", "beta", "agents", "sessions", "retrieve"},
		{"openai", "beta", "agents", "sessions", "events", "create"},
		{"openai", "beta", "agents", "create"},
	} {
		require.NoError(t, root.Run(ctx, args))
	}
	require.Len(t, calls, 5)
	require.Len(t, cells, 2, "only exact session creation gains an intent cell")
	require.NotSame(t, cells[0], cells[1])
	require.False(t, agentsSessionCreationWithoutInput(ctx))
	require.False(t, agentsSessionCreationWithoutInput(nil))
}

func TestAgentsSessionCreationMiddlewareCapturesOnlyResolvedIntent(t *testing.T) {
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("synthetic-creation-key"), option.WithMaxRetries(0))
	var mu sync.Mutex
	var cells []*agentsSessionCreationIntent
	type bodyKey struct{}
	type markerKey struct{}
	const accepted = `{"environment":{"type":"self_hosted"},"input":null}`
	const strict = `{"environment":{"type":"self_hosted"},"input":"null"}`
	command := &cli.Command{Name: "create", Action: func(ctx context.Context, _ *cli.Command) error {
		cell := ctx.Value(agentsSessionCreationKey{}).(*agentsSessionCreationIntent)
		if cell.withoutInput.Load() || ctx.Value(markerKey{}) != "preserved" {
			return errors.New("action inherited mutable intent or lost context")
		}
		body := []byte(ctx.Value(bodyKey{}).(string))
		want := string(body) == accepted
		if !want {
			cell.withoutInput.Store(true) // A final strict request must clear earlier intent.
		}
		requestOptions := agentsSessionCreationRequestOptions(body)
		// Returned options must retain only the Boolean, not the source byte slice.
		for i := range body {
			body[i] = 'x'
		}
		requestOptions = append([]option.RequestOption{option.WithRequestBody("application/json", []byte(ctx.Value(bodyKey{}).(string)))}, requestOptions...)
		stream := client.Beta.Agents.Sessions.NewStreaming(ctx, openai.BetaAgentSessionNewParams{}, requestOptions...)
		defer stream.Close()
		if err := stream.Err(); err != nil {
			return err
		}
		if agentsSessionCreationWithoutInput(ctx) != want {
			return errors.New("stream presentation started before the resolved intent was available")
		}
		mu.Lock()
		cells = append(cells, cell)
		mu.Unlock()
		return nil
	}}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "beta:agents:sessions", Commands: []*cli.Command{command}}}}
	configureAgentsSessionCreation(root)
	var workers sync.WaitGroup
	errorsFound := make(chan error, 8)
	for i := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			body := accepted
			if i%2 != 0 {
				body = strict
			}
			ctx := context.WithValue(context.WithValue(context.Background(), markerKey{}, "preserved"), bodyKey{}, body)
			errorsFound <- command.Action(ctx, command)
		}()
	}
	workers.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
	require.Len(t, cells, 8)
	seen := map[*agentsSessionCreationIntent]bool{}
	for _, cell := range cells {
		require.False(t, seen[cell], "concurrent actions must not share intent")
		seen[cell] = true
	}
}

const agentsIdleCreationAck = `{"type":"agent.session.created","session":{"id":"sess_test","status":"idle","error":null,"environment":{"id":"env_test","type":"self_hosted"}}}`

func TestAgentsSessionCreationCompletionPreservesStrongerErrors(t *testing.T) {
	for _, tc := range []struct {
		name              string
		ctx               context.Context
		readErr, closeErr error
	}{
		{name: "transport", readErr: io.ErrUnexpectedEOF},
		{name: "close", closeErr: errors.New("synthetic close failure")},
		{name: "cancelled", ctx: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &agentsOwnedStream{values: []gjson.Result{gjson.Parse(agentsIdleCreationAck)}, readErr: tc.readErr, closeErr: tc.closeErr}
			route := transformers.Route{Operation: "(resource) beta.agents.sessions > (method) create", OutputKind: OutputStreamEvent}
			stream := &agentsStream[outputJSON]{source: source, route: route, state: transformers.AgentsStreamState{AllowNoInputSessionCreation: true}}
			var out bytes.Buffer
			err := showJSONIterator(stream, -1, ShowJSONOpts{Context: tc.ctx, Operation: route.Operation, OutputKind: OutputStreamEvent, Format: "jsonl", ExplicitFormat: true, Stdout: &out}, transformers.Select)
			if tc.ctx != nil {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				want := tc.readErr
				if want == nil {
					want = tc.closeErr
				}
				require.ErrorIs(t, err, want)
			}
			require.Equal(t, 1, source.closes)
		})
	}
}
