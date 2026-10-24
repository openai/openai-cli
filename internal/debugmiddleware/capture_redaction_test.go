package debugmiddleware

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONCaptureRedactsValuesAndPreservesStructure(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{
			name:  "root controls and sensitive input",
			input: `{"model":"private-model","stream":true,"store":false,"temperature":0.25,"max_output_tokens":32,"input":[{"role":"user","content":"private-prompt"}]}`,
			want:  `{"model":"<REDACTED>","stream":true,"store":false,"temperature":0.25,"max_output_tokens":32,"input":"<REDACTED>"}`,
		},
		{
			name:  "list response data arrays",
			input: `{"object":"list","data":[{"id":"private-id","object":"model","created":123,"owned_by":"private-owner"},{"id":"other-id","object":"model"}],"has_more":false}`,
			want:  `{"object":"<REDACTED>","data":[{"id":"<REDACTED>","object":"<REDACTED>","created":"<REDACTED>","owned_by":"<REDACTED>"},{"id":"<REDACTED>","object":"<REDACTED>"}],"has_more":false}`,
		},
		{
			name:  "response event and usage details",
			input: `{"type":"response.completed","sequence_number":7,"response":{"id":"private-id","output":[{"text":"private-output"}],"usage":{"input_tokens":4,"output_tokens":8,"total_tokens":12,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":3}}}}`,
			want:  `{"type":"<REDACTED>","sequence_number":7,"response":{"id":"<REDACTED>","output":"<REDACTED>","usage":{"input_tokens":4,"output_tokens":8,"total_tokens":12,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":3}}}}`,
		},
		{
			name:  "root usage",
			input: `{"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7,"completion_tokens_details":{"accepted_prediction_tokens":1,"rejected_prediction_tokens":2}}}`,
			want:  `{"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7,"completion_tokens_details":{"accepted_prediction_tokens":1,"rejected_prediction_tokens":2}}}`,
		},
		{
			name:  "nested controls have no numeric or boolean privilege",
			input: `{"data":{"stream":true,"max_tokens":1234,"usage":{"total_tokens":5678}},"private-key":{"stream":false}}`,
			want:  `{"data":{"stream":"<REDACTED>","max_tokens":"<REDACTED>","usage":{"total_tokens":"<REDACTED>"}},"<REDACTED>":"<REDACTED>"}`,
		},
		{
			name:  "wrong types cannot inherit privileges",
			input: `{"stream":123,"max_tokens":true,"max_output_tokens":[987],"usage":{"total_tokens":false,"input_tokens":{"total_tokens":654}},"data":[true,321,null]}`,
			want:  `{"stream":"<REDACTED>","max_tokens":"<REDACTED>","max_output_tokens":["<REDACTED>"],"usage":{"total_tokens":"<REDACTED>","input_tokens":{"total_tokens":"<REDACTED>"}},"data":["<REDACTED>","<REDACTED>",null]}`,
		},
		{
			name:  "unknown subtrees and usage names",
			input: `{"private-name":{"data":{"usage":{"total_tokens":12345}}},"usage":{"private-counter":98765,"total_tokens":"private-numeric-string"},"metadata":{"input_tokens":555}}`,
			want:  `{"<REDACTED>":"<REDACTED>","usage":{"<REDACTED>":"<REDACTED>","total_tokens":"<REDACTED>"},"metadata":"<REDACTED>"}`,
		},
		{
			name:  "duplicate keys and order",
			input: `{"stream":true,"model":"first-model","stream":false,"model":"second-model","private-a":1,"private-b":2}`,
			want:  `{"stream":true,"model":"<REDACTED>","stream":false,"model":"<REDACTED>","<REDACTED>":"<REDACTED>","<REDACTED>":"<REDACTED>"}`,
		},
		{
			name:  "escaped known keys and number spelling",
			input: `{"str\u0065am":true,"temperature":1e-2,"seed":-0,"input":"private"}`,
			want:  `{"stream":true,"temperature":1e-2,"seed":-0,"input":"<REDACTED>"}`,
		},
		{name: "string root", input: `"private-root"`, want: `"<REDACTED>"`},
		{name: "number root", input: `123456`, want: `"<REDACTED>"`},
		{name: "boolean root", input: `true`, want: `"<REDACTED>"`},
		{name: "null root", input: `null`, want: `null`},
		{name: "array root", input: `["private",42,false,null]`, want: `["<REDACTED>","<REDACTED>","<REDACTED>",null]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(test.input)
			original := bytes.Clone(input)
			got := redactJSONCapture(input, false)
			if got != test.want {
				t.Errorf("redacted capture = %q, want %q", got, test.want)
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("complete capture is not JSON: %q", got)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("redaction changed the captured input")
			}
			assertJSONCaptureSafe(t, input, got)
		})
	}
}

func TestJSONCaptureCollapsesSensitiveSubtrees(t *testing.T) {
	for _, key := range []string{
		"input", "messages", "message", "content", "output", "text", "delta", "instructions",
		"prompt", "arguments", "parameters", "metadata", "tools", "tool_calls", "files", "file",
		"filename", "file_data", "file_url", "url", "image_url", "audio", "image", "b64_json",
		"embedding", "refusal", "annotations", "logprobs", "error", "headers", "authorization",
		"api_key", "access_token", "secret", "client_secret", "webhook_secret",
	} {
		t.Run(key, func(t *testing.T) {
			input := []byte(`{"` + key + `":{"private-name":["sk-fake-sensitive-capture",{"usage":{"total_tokens":12345}}]}}`)
			want := `{"` + key + `":"<REDACTED>"}`
			if got := redactJSONCapture(input, false); got != want {
				t.Errorf("capture = %q, want %q", got, want)
			}
		})
	}
}

func TestJSONCaptureControlsAndPrivateKeys(t *testing.T) {
	inputs := []string{
		`{"\u001b]52;private\u0007":"private-value","model":"\u001b[31mprivate-value\n"}`,
		`{"mo\ndel":"private-value","Input":"private-value","STR EAM":true}`,
		`{"https://private.test/?token=sk-fake-capture":{"stream":true},"id":"private-\u2028-value"}`,
		`{"model":"` + string([]byte{0xff, 0xfe}) + `private-value","` + string([]byte{0xff}) + `":42}`,
	}
	for _, input := range inputs {
		got := redactJSONCapture([]byte(input), false)
		for _, private := range []string{"private", "sk-fake", "https://", "token="} {
			if strings.Contains(got, private) {
				t.Errorf("capture disclosed %q: %q", private, got)
			}
		}
		assertJSONCaptureSafe(t, []byte(input), got)
	}
}

func TestJSONCaptureMalformedInputNeverReturnsRawPrefix(t *testing.T) {
	for _, input := range []string{
		"", " ", "private-prompt", `{"model":"sk-fake-unterminated`, `{"stream":true,}`,
		`{"stream":true} {"input":"private"}`, `{"max_tokens":NaN}`, `{"max_tokens":01}`,
		"{\"model\":\"private\nraw-control\"}", `{"model":"\uZZZZprivate"}`,
	} {
		for _, truncated := range []bool{false, true} {
			want := jsonCaptureInvalid
			if truncated {
				want = jsonCaptureTruncated
			}
			got := redactJSONCapture([]byte(input), truncated)
			if got != want {
				t.Errorf("truncated=%t: capture = %q, want fixed omission %q", truncated, got, want)
			}
			assertJSONCaptureSafe(t, []byte(input), got)
		}
	}
}

func TestJSONCaptureEveryTruncatedPrefixIsSanitized(t *testing.T) {
	input := []byte(`{"stream":true,"input":"sk-fake-split-private\u001b","data":[{"id":"private-☃"}],"usage":{"total_tokens":17}}`)
	for size := 0; size <= len(input); size++ {
		prefix := input[:size]
		got := redactJSONCapture(prefix, true)
		if !json.Valid(prefix) {
			if got != jsonCaptureTruncated {
				t.Fatalf("prefix %d exposed incomplete JSON: %q", size, got)
			}
		} else if !strings.HasSuffix(got, jsonCaptureSuffix) {
			t.Fatalf("prefix %d omitted its truncation marker: %q", size, got)
		}
		for _, private := range []string{"sk-fake", "private", "☃"} {
			if strings.Contains(got, private) {
				t.Fatalf("prefix %d disclosed %q: %q", size, private, got)
			}
		}
		assertJSONCaptureSafe(t, prefix, got)
	}
	got := redactJSONCapture([]byte(`{"stream":true}`), true)
	if want := `{"stream":true}` + jsonCaptureSuffix; got != want {
		t.Errorf("valid captured prefix = %q, want %q", got, want)
	}
}

func TestJSONCaptureRenderingLimits(t *testing.T) {
	expanding := []byte(`{"data":[` + strings.Repeat(`{"id":""},`, 100) + `{"id":""}]}`)
	deep := []byte(strings.Repeat(`{"data":`, 80) + `"private"` + strings.Repeat("}", 80))
	for _, input := range [][]byte{expanding, deep} {
		got := redactJSONCapture(input, false)
		if got != jsonCaptureLimited {
			t.Errorf("bounded renderer returned %q, want fixed omission", got)
		}
		assertJSONCaptureSafe(t, input, got)
		got = redactJSONCapture(input, true)
		if want := jsonCaptureLimited + jsonCaptureSuffix; got != want {
			t.Errorf("truncated limited capture = %q, want %q", got, want)
		}
		assertJSONCaptureSafe(t, input, got)
	}
	// A hidden deep subtree uses an iterative skip rather than renderer recursion.
	hidden := []byte(`{"input":` + strings.Repeat("[", 500) + `"private"` + strings.Repeat("]", 500) + `,"stream":true}`)
	if got, want := redactJSONCapture(hidden, false), `{"input":"<REDACTED>","stream":true}`; got != want {
		t.Errorf("hidden deep subtree = %q, want %q", got, want)
	}
	// The largest accepted capture remains a diagnostic input, not an API cap.
	large := []byte(`{"input":"` + strings.Repeat("x", (1<<20)-12) + `"}`)
	if got := redactJSONCapture(large, false); got != `{"input":"<REDACTED>"}` {
		t.Errorf("large sensitive input did not collapse: %q", got)
	}
}

func assertJSONCaptureSafe(t *testing.T, input []byte, output string) {
	t.Helper()
	if len(output) > len(input)+128 {
		t.Errorf("output has %d bytes, exceeds %d-byte diagnostic bound", len(output), len(input)+128)
	}
	for _, b := range []byte(output) {
		if b < 0x20 || b > 0x7e {
			t.Fatalf("capture contains a non-ASCII or terminal-control byte: %q", output)
		}
	}
}
