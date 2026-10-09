package transformers_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/openai/openai-cli/pkg/custom"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const persistedSummaryHint = "Summary; use --format json for full data.\n"

var agentsItemResources = []string{
	"beta.agents.sessions.items",
	"beta.agents.sessions.turns.items",
	"beta.agents.sessions.subagents.items",
	"beta.agents.sessions.subagents.turns.items",
}

type persistedRecord string

func (r persistedRecord) RawJSON() string { return string(r) }

type persistedRows struct {
	values []persistedRecord
	index  int
	err    error
}

func (r *persistedRows) Next() bool               { r.index++; return r.index <= len(r.values) }
func (r *persistedRows) Current() persistedRecord { return r.values[r.index-1] }
func (r *persistedRows) Err() error               { return r.err }

func persistedImageRecord(kind, encoded string) (string, string, string) {
	url := "data:image/png;base64," + encoded
	content := `[{"type":"input_text","text":"Before image"},{"type":"input_image","image_url":` + strconv.Quote(url) + `,"future_image":9007199254740993},{"type":"input_text","text":"After image"}]`
	switch kind {
	case "message":
		return `{"type":"message","id":null,"turn_id":"turn_test","role":"user","status":"completed","phase":null,"content":` + content + `,"future_item":null}`, "content.1.image_url", url
	case "function_call_output":
		return `{"type":"function_call_output","id":"item_test","turn_id":"turn_test","call_id":"call_test","status":"completed","error":null,"output":` + content + `,"future_item":null}`, "output.1.image_url", url
	default:
		url = "data:image/jpeg;base64," + encoded
		return `{"type":"computer_use_call","id":"item_test","turn_id":"turn_test","title":"Inspect screenshot","status":"completed","output":{"type":"computer_screenshot","image_url":` + strconv.Quote(url) + `,"future_image":9007199254740993},"future_item":null}`, "output.image_url", url
	}
}

func showPersistedRecord(value, resource, method string, opts custom.ShowJSONOpts) error {
	opts.Operation = "(resource) " + resource + " > (method) " + method
	if method == "list" {
		opts.OutputKind = custom.OutputPageItem
		return custom.ShowJSONIterator(&persistedRows{values: []persistedRecord{persistedRecord(value)}}, -1, opts)
	}
	opts.OutputKind = custom.OutputResponse
	return custom.ShowJSON(gjson.Parse(value), opts)
}

func TestAgentsPersistedImagesHumanRoutes(t *testing.T) {
	encoded := strings.Repeat("QUJD", 32768)
	for _, resource := range agentsItemResources {
		for _, kind := range []string{"message", "function_call_output", "computer_use_call"} {
			for _, format := range []string{"auto", "text"} {
				t.Run(resource+"/"+kind+"/"+format, func(t *testing.T) {
					value, _, _ := persistedImageRecord(kind, encoded)
					var out, diagnostic bytes.Buffer
					require.NoError(t, showPersistedRecord(value, resource, "list", custom.ShowJSONOpts{
						Format: format, Stdout: &out, Stderr: &diagnostic,
					}))
					if strings.Contains(out.String(), encoded) {
						t.Fatalf("human resource output retained %d encoded characters", len(encoded))
					}
					require.Contains(t, out.String(), "131072 base64 characters")
					require.Contains(t, out.String(), "9007199254740993")
					require.Contains(t, out.String(), "Future item: (null)")
					require.Contains(t, out.String(), "turn_test")
					if kind != "computer_use_call" {
						require.Less(t, strings.Index(out.String(), "Before image"), strings.Index(out.String(), "131072 base64 characters"))
						require.Less(t, strings.Index(out.String(), "131072 base64 characters"), strings.Index(out.String(), "After image"))
					}
					require.Equal(t, persistedSummaryHint, diagnostic.String())
				})
			}
		}
	}
}

func persistedSubagentRecord() string {
	parts := make([]string, 2000)
	for i := range parts {
		if i%2 == 0 {
			parts[i] = `{"type":"output_text","text":"synthetic task instructions"}`
		} else {
			parts[i] = `{"type":"encrypted_content","encrypted_content":"synthetic encrypted instructions"}`
		}
	}
	return `{"id":"sub_test","object":"agent.session.subagent","session_id":"sess_test","name":"Researcher","instructions":[` + strings.Join(parts, ",") + `],"parent_agent_id":"parent_test","status":"closed","opened_at":1728000000,"closed_at":1728000001}`
}

func TestAgentsSubagentResourcesSummarizeInstructions(t *testing.T) {
	for _, method := range []string{"list", "retrieve"} {
		for _, format := range []string{"auto", "text"} {
			t.Run(method+"/"+format, func(t *testing.T) {
				var out, diagnostic bytes.Buffer
				require.NoError(t, showPersistedRecord(persistedSubagentRecord(), "beta.agents.sessions.subagents", method,
					custom.ShowJSONOpts{Format: format, Stdout: &out, Stderr: &diagnostic}))
				if strings.Contains(out.String(), "synthetic task instructions") || strings.Contains(out.String(), "synthetic encrypted instructions") {
					t.Fatal("human subagent output retained required instructions")
				}
				for _, field := range []string{"sub_test", "sess_test", "Researcher", "parent_test", "closed"} {
					require.Contains(t, out.String(), field)
				}
				require.NotContains(t, out.String(), "1728000000")
				require.Equal(t, persistedSummaryHint, diagnostic.String())
			})
		}
	}
}

func TestAgentsPersistedResourcesPreserveExplicitData(t *testing.T) {
	for _, kind := range []string{"message", "function_call_output", "computer_use_call", "subagent"} {
		value, path, expected := persistedImageRecord(kind, "QUJDRA==")
		resource, method := agentsItemResources[0], "list"
		if kind == "subagent" {
			value, path, expected = persistedSubagentRecord(), "instructions.0.text", "synthetic task instructions"
			resource, method = "beta.agents.sessions.subagents", "retrieve"
		}
		for _, format := range []string{"json", "jsonl", "pretty", "raw", "yaml", "explore"} {
			t.Run(kind+"/"+format, func(t *testing.T) {
				var out, diagnostic bytes.Buffer
				require.NoError(t, showPersistedRecord(value, resource, method, custom.ShowJSONOpts{
					Format: format, ExplicitFormat: true, Stdout: &out, Stderr: &diagnostic,
				}))
				require.Contains(t, out.String(), expected)
				require.NotContains(t, diagnostic.String(), persistedSummaryHint)
				if format != "yaml" && format != "pretty" {
					require.Equal(t, expected, gjson.Get(out.String(), path).Str)
				}
				if format == "raw" || format == "jsonl" {
					require.Equal(t, value+"\n", out.String())
				}
			})
		}
		t.Run(kind+"/extraction", func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			require.NoError(t, showPersistedRecord(value, resource, method, custom.ShowJSONOpts{
				Format: "text", Transform: path, RawOutput: true, Stdout: &out, Stderr: &diagnostic,
			}))
			require.Equal(t, expected+"\n", out.String())
			require.Empty(t, diagnostic.String())
		})
	}
}

func TestAgentsPersistedResourcesHintPolicyAndPartialFailure(t *testing.T) {
	for _, flags := range [][]string{nil, {"--quiet"}, {"--format-error", "json"}, {"--transform-error", "error.message"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			value, _, _ := persistedImageRecord("message", "QUJDRA==")
			pageErr := errors.New("synthetic later page error")
			rows := &persistedRows{values: []persistedRecord{persistedRecord(value), persistedRecord(value)}, err: pageErr}
			root := &cli.Command{Name: "openai", Flags: []cli.Flag{
				&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"},
			}, Action: func(ctx context.Context, _ *cli.Command) error {
				return custom.ShowJSONIterator(rows, -1, custom.ShowJSONOpts{
					Context: ctx, Operation: "(resource) beta.agents.sessions.items > (method) list", OutputKind: custom.OutputPageItem,
					Stdout: &out, Stderr: &diagnostic,
				})
			}}
			custom.ConfigureCommand(root)
			require.ErrorIs(t, root.Run(t.Context(), append([]string{"openai"}, flags...)), pageErr)
			require.Equal(t, 2, strings.Count(out.String(), "8 base64 characters"))
			if len(flags) == 0 {
				require.Equal(t, persistedSummaryHint, diagnostic.String())
			} else {
				require.Empty(t, diagnostic.String())
			}
		})
	}
}

func TestAgentsSubagentResourcesUnknownFieldsKeepFullData(t *testing.T) {
	value := strings.TrimSuffix(persistedSubagentRecord(), "}") + `,"future_setting":null}`
	for _, method := range []string{"list", "retrieve"} {
		t.Run(method, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			require.NoError(t, showPersistedRecord(value, "beta.agents.sessions.subagents", method,
				custom.ShowJSONOpts{Stdout: &out, Stderr: &diagnostic}))
			require.Contains(t, out.String(), "synthetic task instructions")
			require.Contains(t, out.String(), "synthetic encrypted instructions")
			require.Contains(t, out.String(), "Future setting: (null)")
			require.Empty(t, diagnostic.String())
		})
	}
}
