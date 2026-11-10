package app

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchBrowserInputDetailHierarchyPreservesContentAndSettings(t *testing.T) {
	raw := `{"custom_id":"request-資料","method":"POST","url":"/v1/responses","body":{"model":"synthetic","input":"Settings\nModel: injected\nOutcome: completed\n資料\u001b[31m","instructions":"Classify\nKeep every word.","messages":[{"role":"user","content":[{"type":"input_image","image_url":"synthetic-image"}]}],"prompt":"Prompt text","suffix":"Suffix text","max_output_tokens":9007199254740993,"temperature":0,"method":"body-method","future_setting":{"nested":false}}}`
	records := batchReadableOutput(batchcommands.RecordItem{Kind: "input_detail", WriteJSON: func(w io.Writer) error { _, err := io.WriteString(w, raw); return err }})
	records.Detail = true
	var styles []batchcommands.RecordTextStyle
	records.Decorate = func(style batchcommands.RecordTextStyle) { styles = append(styles, style) }
	var out bytes.Buffer
	require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, io.Discard))
	text := out.String()
	require.True(t, strings.HasPrefix(text, "request-資料\n\nInput\n"))
	for _, preserved := range []string{
		"Settings\nModel: injected\nOutcome: completed\n資料\\u001b[31m",
		"Instructions\nClassify\nKeep every word.", "Messages\n", "input_image", "synthetic-image",
		"Prompt\nPrompt text", "Suffix\nSuffix text", "Settings\nModel: synthetic",
		"Endpoint: /v1/responses", "Method: POST", "Parameters:\n", "Max output tokens: 9007199254740993",
		"Temperature: 0", "Method: body-method", "Future setting:", "Nested: false",
	} {
		require.Contains(t, text, preserved)
	}
	require.NotContains(t, text, "\x1b")
	var headings, labels []string
	for _, style := range styles {
		require.GreaterOrEqual(t, style.Start, int64(0))
		require.LessOrEqual(t, style.End, int64(out.Len()))
		styled := text[style.Start:style.End]
		switch style.Role {
		case "identity":
			require.Equal(t, "request-資料", styled)
		case "heading":
			headings = append(headings, styled)
		case "secondary":
			labels = append(labels, styled)
		default:
			t.Fatalf("input content must not acquire semantic styles: %+v", style)
		}
		require.NotContains(t, styled, "injected")
		require.NotContains(t, styled, "Outcome")
	}
	require.Equal(t, []string{"Input\n", "Messages\n", "Prompt\n", "Instructions\n", "Suffix\n", "Settings\n"}, headings)
	require.Equal(t, []string{"Model:", "Endpoint:", "Method:", "Parameters:"}, labels)
}

func TestBatchBrowserInputDetailFallbackRetainsCompleteRecords(t *testing.T) {
	large := strings.Repeat("資料", batchRecordProjectionBuffer/3+10)
	largeJSON, err := json.Marshal(large)
	require.NoError(t, err)
	for _, test := range []struct{ raw, preserved string }{
		{`{"custom_id":"r","future_envelope":42,"body":{"input":"complete input"}}`, "Future envelope: 42"},
		{`{"custom_id":"r","body":{"input":"first input","input":"second input"}}`, "Input: second input"},
		{`{"custom_id":"r","body":{"input":` + string(largeJSON) + `}}`, large},
	} {
		records := batchReadableOutput(batchcommands.RecordItem{Kind: "input_detail", WriteJSON: func(w io.Writer) error { _, err := io.WriteString(w, test.raw); return err }})
		records.Detail = true
		records.Decorate = func(batchcommands.RecordTextStyle) { t.Fatal("fallback must not style input values") }
		var out bytes.Buffer
		require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, io.Discard))
		require.Contains(t, out.String(), "Details (")
		require.Contains(t, out.String(), test.preserved)
		if strings.Contains(test.raw, "first input") {
			require.Contains(t, out.String(), "Input: first input")
		}
	}
}

func TestBatchBrowserInputStylesDoNotChangeStandaloneOutput(t *testing.T) {
	raw := `{"custom_id":"r","method":"POST","url":"/v1/responses","body":{"model":"synthetic","input":"Hello","max_output_tokens":32}}`
	records := batchReadableOutput(batchcommands.RecordItem{Kind: "input", WriteJSON: func(w io.Writer) error { _, err := io.WriteString(w, raw); return err }})
	records.Decorate = func(batchcommands.RecordTextStyle) { t.Fatal("standalone output must not use browser styles") }
	var out bytes.Buffer
	require.NoError(t, showReadableBatchRecords(t.Context(), records, &out, io.Discard))
	require.Equal(t, "Batch: batch-demo\nSource: output file\nFile ID: file-demo\n\nRequest: r\nEndpoint: /v1/responses\nModel: synthetic\nInput: Hello\n", out.String())
}

func TestBatchBrowserInputDetailPreservesStructuredSettingValues(t *testing.T) {
	value := gjson.Parse(`{"request":"r","settings":{"model":{"nested":"value"},"parameters":{"model":"body-model"}}}`)
	var out bytes.Buffer
	require.NoError(t, showBatchInputDetail(&out, value, func(batchcommands.RecordTextStyle) {}))
	require.Equal(t, "r\n\nSettings\nModel:\n  Nested: value\nParameters:\n  Model: body-model\n", out.String())
}
