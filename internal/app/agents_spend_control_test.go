package app

import (
	"bytes"
	"strings"
	"testing"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const agentsSpendSession = `{"id":"sess_spend","object":"agent.session","status":"idle","error":null,"required_actions":[],"usage":null,"created_at":1728000000,"last_active_at":1728000001,"agent":{"id":"agent_spend","instructions":"synthetic nested instructions","tools":[{"type":"function","name":"synthetic_tool"}]},"environment":{"id":"env_spend","type":"self_hosted"},"metadata":{"purpose":"synthetic metadata"},"vault_ids":[]}`

type agentsSpendRoute struct {
	name, operation string
	list, stream    bool
}

var agentsSpendRoutes = []agentsSpendRoute{
	{"create", "(resource) beta.agents.sessions > (method) create", false, false},
	{"retrieve", "(resource) beta.agents.sessions > (method) retrieve", false, false},
	{"update", "(resource) beta.agents.sessions > (method) update", false, false},
	{"list", "(resource) beta.agents.sessions > (method) list", true, false},
	{"created", "(resource) beta.agents.sessions > (method) create", false, true},
	{"idle", "(resource) beta.agents.sessions.events > (method) stream", false, true},
}

func (route agentsSpendRoute) value(spend string) string {
	session := agentsSpendSession
	if spend != "" {
		session = strings.TrimSuffix(session, "}") + `,"spend_control":` + spend + `}`
	}
	if route.stream {
		return `{"type":"agent.session.` + route.name + `","session_id":"sess_spend","session":` + session + `}`
	}
	return session
}

func (route agentsSpendRoute) show(value string, opts ShowJSONOpts) error {
	opts.Operation, opts.OutputKind = route.operation, OutputResponse
	if route.list {
		opts.OutputKind = OutputPageItem
		return ShowJSONIterator(streamItems(value), -1, opts)
	}
	if route.stream {
		opts.OutputKind = OutputStreamEvent
		// A local observation limit isolates lifecycle presentation from turn completion.
		return ShowJSONIterator(streamItems(value), 1, opts)
	}
	return ShowJSON(gjson.Parse(value), opts)
}

func TestAgentsSpendControlReadableAndMachinePresentation(t *testing.T) {
	for _, route := range agentsSpendRoutes {
		for _, spend := range []struct{ name, value, consumed string }{
			{"unlimited", "", ""},
			{"zero consumed", `{"limit":200,"consumed":0}`, "0"},
			{"unknown consumed", `{"limit":200,"consumed":null}`, "(null)"},
			{"recorded spend", `{"limit":200,"consumed":37}`, "37"},
		} {
			for _, format := range []string{"auto", "text", "json", "jsonl"} {
				t.Run(route.name+"/"+spend.name+"/"+format, func(t *testing.T) {
					var stdout, stderr bytes.Buffer
					value := route.value(spend.value)
					require.NoError(t, route.show(value, ShowJSONOpts{
						Format: format, ExplicitFormat: format != "auto", Stdout: &stdout, Stderr: &stderr,
					}))
					if format == "json" || format == "jsonl" {
						expected := value
						if route.list && format == "json" {
							expected = `[` + value + `]`
						}
						require.JSONEq(t, expected, stdout.String(), "machine output must preserve spending and nested configuration")
						require.Empty(t, stderr.String())
						return
					}
					require.Contains(t, stdout.String(), "sess_spend")
					require.NotContains(t, stdout.String(), "synthetic nested instructions")
					require.NotContains(t, stdout.String(), "synthetic_tool")
					require.NotContains(t, stdout.String(), "synthetic metadata")
					if spend.value == "" {
						require.NotContains(t, stdout.String(), "Spend control")
					} else {
						require.Contains(t, stdout.String(), "Spend control:")
						require.Contains(t, stdout.String(), "Limit: 200")
						require.Contains(t, stdout.String(), "Consumed: "+spend.consumed)
					}
					hint := clioutput.ResourceSummaryHint + "\n"
					if route.stream {
						hint = "Some fields omitted. Use --format jsonl for complete events.\n"
					}
					require.Equal(t, hint, stderr.String(), "configuration omission must retain its recovery hint")
				})
			}
		}
	}
}

func TestAgentsSpendControlExtractionPreservesSpendingAndConfiguration(t *testing.T) {
	for _, route := range agentsSpendRoutes {
		value := route.value(`{"limit":200,"consumed":null}`)
		for _, field := range []string{"spend_control", "agent"} {
			t.Run(route.name+"/"+field, func(t *testing.T) {
				path := field
				if route.stream {
					path = "session." + field
				}
				var stdout, stderr bytes.Buffer
				require.NoError(t, route.show(value, ShowJSONOpts{
					Format: "jsonl", ExplicitFormat: true, Transform: path, Stdout: &stdout, Stderr: &stderr,
				}))
				require.JSONEq(t, gjson.Get(value, path).Raw, stdout.String())
				require.Empty(t, stderr.String())
			})
		}
	}
}
