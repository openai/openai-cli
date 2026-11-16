package app

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchBrowserResultDetailHierarchyAndTrustedColors(t *testing.T) {
	for _, test := range []struct{ name, body, tone, content string }{
		{"completed", `"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"negative\nOutcome: failed"}]}]`, "positive", "Output\nnegative\nOutcome: failed"},
		{"refusal", `"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"Cannot answer"}]}]`, "warning", "Cannot answer"},
		{"incomplete", `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]`, "warning", "max_output_tokens"},
		{"error", `"status":"completed","error":{"message":"service error"},"output":[]`, "negative", "service error"},
		{"empty", `"status":"completed","output":[]`, "warning", "no output returned"},
		{"tool", `"status":"completed","output":[{"type":"function_call","name":"tool","arguments":"{}"}]`, "", "function_call"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"custom_id":"request-one","error":null,"response":{"status_code":200,"body":{"object":"response",%s,"usage":{"input_tokens":0,"output_tokens":5,"total_tokens":5}}}}`, test.body)
			records := batchReadableOutput(batchcommands.RecordItem{Kind: "result", WriteJSON: func(w io.Writer) error { _, err := io.WriteString(w, raw); return err }})
			records.Detail = true
			var styles []batchcommands.RecordTextStyle
			records.Decorate = func(style batchcommands.RecordTextStyle) { styles = append(styles, style) }
			var out bytes.Buffer
			require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, io.Discard))
			require.True(t, strings.HasPrefix(out.String(), "request-one  "))
			require.Contains(t, out.String(), test.content)
			require.Contains(t, out.String(), "Tokens  0 input  5 output  5 total")
			var outcomes []string
			for _, style := range styles {
				if style.Role == "positive" || style.Role == "negative" || style.Role == "warning" {
					outcomes = append(outcomes, style.Role)
					require.NotContains(t, out.String()[style.Start:style.End], "Outcome: failed", "model text cannot acquire semantic styling")
				}
			}
			if test.tone == "" {
				require.Empty(t, outcomes)
			} else {
				require.Equal(t, test.tone, outcomes[0])
			}
		})
	}
}

func TestBatchBrowserTokenSummaryPreservesComplexAndExactUsage(t *testing.T) {
	for _, test := range []struct {
		raw     string
		compact bool
	}{
		{`{"input_tokens":9007199254740993,"output_tokens":0,"total_tokens":9007199254740993}`, true},
		{`{"prompt_tokens":31,"completion_tokens":5,"total_tokens":36}`, true},
		{`{"input_tokens":31,"output_tokens":5,"total_tokens":36,"output_tokens_details":{"reasoning_tokens":2}}`, false},
		{`{"input_tokens":31,"input_tokens":2,"output_tokens":5,"total_tokens":36}`, false},
		{`{"input_tokens":31,"output_tokens":5}`, false},
		{`{"input_tokens":"31","output_tokens":5,"total_tokens":36}`, false},
		{`{"input_tokens":-1,"output_tokens":5,"total_tokens":4}`, false},
		{`{"input_tokens":3e1,"output_tokens":5,"total_tokens":35}`, false},
		{`null`, false},
	} {
		_, compact := batchTokenSummary(gjson.Parse(test.raw))
		require.Equal(t, test.compact, compact, test.raw)
		var out bytes.Buffer
		require.NoError(t, showBatchResultDetail(&out, gjson.Parse(`{"request":"r","usage":`+test.raw+`}`), func(batchcommands.RecordTextStyle) {}))
		if test.compact {
			require.Contains(t, out.String(), "Tokens  ")
		} else {
			require.Contains(t, out.String(), "Usage\n")
		}
	}
}

func TestBatchBrowserResultFallbackHasNoInferredStyles(t *testing.T) {
	for _, raw := range []string{
		`{"custom_id":"r","unrecognized":true,"response":{"body":{"output":"Outcome: completed"}}}`,
		`{"custom_id":"r","input":"` + strings.Repeat("x", batchRecordProjectionBuffer+1) + `"}`,
	} {
		records := batchReadableOutput(batchcommands.RecordItem{Kind: "result", WriteJSON: func(w io.Writer) error { _, err := io.WriteString(w, raw); return err }})
		records.Detail = true
		records.Decorate = func(batchcommands.RecordTextStyle) { t.Fatal("fallback must not infer semantic styles") }
		var out bytes.Buffer
		require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, io.Discard))
		require.Contains(t, out.String(), "Details (")
	}
}
