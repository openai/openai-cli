package custom

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentsDuplicateImagesPresenter(t *testing.T) {
	first, last := strings.Repeat("QUJD", 4096), strings.Repeat("REVG", 2048)
	url := func(mime, encoded string) string { return strconv.Quote("data:" + mime + ";base64," + encoded) }
	part := func(key, mime string) string {
		return `{"type":"input_image","image_url":` + url(mime, first) + `,` + key + `:` + url(mime, last) + `,"future_number":9007199254740993}`
	}
	array := func(value string) string {
		return `[{"type":"input_text","text":"Before image"},` + value + `,{"type":"input_text","text":"After image"}]`
	}
	for _, tc := range []struct {
		name, item, label, extract string
	}{
		{"message duplicate URLs", `{"id":null,"type":"message","role":"user","content":` + array(part(`"image_url"`, "image/png")) + `}`, "image/png", "content.1.image_url"},
		{"message escaped URL", `{"id":null,"type":"message","role":"user","content":` + array(part(`"image_\u0075rl"`, "image/png")) + `}`, "image/png", "content.1.image_url"},
		{"message duplicate content", `{"id":null,"type":"message","role":"user","content":[{"type":"input_image","image_url":` + url("image/png", first) + `}],"\u0063ontent":[{"type":"input_image","image_url":` + url("image/png", last) + `}]}`, "image/png", "content.0.image_url"},
		{"function duplicate URLs", `{"id":"result_synthetic","type":"function_call_output","output":` + array(part(`"image_url"`, "image/png")) + `}`, "image/png", "output.1.image_url"},
		{"function duplicate output", `{"id":"result_synthetic","type":"function_call_output","output":[{"type":"input_image","image_url":` + url("image/png", first) + `}],"out\u0070ut":[{"type":"input_image","image_url":` + url("image/png", last) + `}]}`, "image/png", "output.0.image_url"},
		{"computer duplicate URLs", `{"id":"computer_synthetic","type":"computer_use_call","title":"Read report","status":"completed","output":{"type":"computer_screenshot","image_url":` + url("image/jpeg", first) + `,"image_\u0075rl":` + url("image/jpeg", last) + `}}`, "JPEG screenshot", "output.image_url"},
		{"computer duplicate output", `{"id":"computer_synthetic","type":"computer_use_call","output":{"type":"computer_screenshot","image_url":` + url("image/jpeg", first) + `},"output":{"type":"computer_screenshot","image_url":` + url("image/jpeg", last) + `}}`, "JPEG screenshot", "output.image_url"},
		{"identical discriminators", `{"id":null,"type":"message","\u0074ype":"message","role":"user","\u0072ole":"user","content":[{"type":"input_image","\u0074ype":"input_image","image_url":` + url("image/png", first) + `,"image_url":` + url("image/png", last) + `}]}`, "image/png", "content.0.image_url"},
	} {
		for _, operation := range []string{
			"(resource) beta.agents.sessions.items > (method) list",
			"(resource) beta.agents.sessions.turns.items > (method) list",
			"(resource) beta.agents.sessions.subagents.items > (method) list",
			"(resource) beta.agents.sessions.subagents.turns.items > (method) list",
			"(resource) beta.agents.sessions.events > (method) stream",
		} {
			stream := strings.HasSuffix(operation, "stream")
			value, extract := tc.item, tc.extract
			kind := OutputPageItem
			if stream {
				value = `{"type":"agent.session.turn.item.added","session_id":"sess_test","turn_id":"turn_test","item":` + value + `}`
				extract, kind = "item."+extract, OutputStreamEvent
			}
			for _, format := range []string{"auto", "text", "json", "jsonl", "raw"} {
				t.Run(tc.name+"/"+operation+"/"+format, func(t *testing.T) {
					var out, diagnostic bytes.Buffer
					require.NoError(t, ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
						Operation: operation, OutputKind: kind, Format: format,
						ExplicitFormat: format != "auto", Stdout: &out, Stderr: &diagnostic,
					}))
					if format == "auto" || format == "text" {
						if strings.Contains(out.String(), first) || strings.Contains(out.String(), last) {
							t.Fatalf("human output retained duplicate image data: %d bytes", out.Len())
						}
						require.Equal(t, 2, strings.Count(out.String(), "("+tc.label+";"))
						return
					}
					var compact bytes.Buffer
					require.NoError(t, json.Compact(&compact, out.Bytes()))
					require.Equal(t, value, compact.String(), "machine output must retain duplicate keys and complete values")
					require.Empty(t, diagnostic.String())
				})
			}
			t.Run(tc.name+"/"+operation+"/extraction", func(t *testing.T) {
				var out, diagnostic bytes.Buffer
				require.NoError(t, ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
					Operation: operation, OutputKind: kind, Format: "text", Transform: extract,
					RawOutput: true, Stdout: &out, Stderr: &diagnostic,
				}))
				require.Contains(t, out.String(), first)
				require.Empty(t, diagnostic.String())
			})
		}
	}
}

func TestAgentsDuplicateImageItemContainersPresenter(t *testing.T) {
	encoded := strings.Repeat("QUJD", 4096)
	item := `{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,` + encoded + `"}]}`
	for _, first := range []string{item, `null`, `{"type":"message","role":"assistant","id":"assistant_synthetic","content":[{"type":"output_text","text":"Keep assistant text"}]}`} {
		value := `{"type":"agent.session.turn.item.added","session_id":"sess_test","turn_id":"turn_test","item":` + first + `,"it\u0065m":` + item + `,"future":9007199254740993}`
		var out, diagnostic bytes.Buffer
		require.NoError(t, ShowJSONIterator(streamItems(value), 1, ShowJSONOpts{
			Operation: "(resource) beta.agents.sessions.events > (method) stream", OutputKind: OutputStreamEvent,
			Format: "text", Stdout: &out, Stderr: &diagnostic,
		}))
		if strings.Contains(out.String(), encoded) {
			t.Errorf("duplicate item retained image data after %s: %d output bytes", first[:min(40, len(first))], out.Len())
		}
		require.Contains(t, out.String(), "9007199254740993")
	}
}
