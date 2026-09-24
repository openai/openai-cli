package readable

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestWriteEndpoints(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, input, want string
	}{
		{
			name:  "model",
			input: `{"id":"gpt-example","object":"model","created":9007199254740993,"owned_by":"openai","api_url":"https://example.invalid/models"}`,
			want:  "ID: gpt-example\nObject: model\nCreated: 9007199254740993\nOwned by: openai\nAPI URL: https://example.invalid/models\n",
		},
		{
			name:  "files",
			input: `{"object":"list","data":[{"id":"file-one","filename":"notes.txt","bytes":12},{"id":"file-two","filename":"résumé.txt","bytes":24}],"has_more":false}`,
			want: "Object: list\nData:\n" +
				"  1.\n    ID: file-one\n    Filename: notes.txt\n    Bytes: 12\n\n" +
				"  2.\n    ID: file-two\n    Filename: résumé.txt\n    Bytes: 24\nHas more: false\n",
		},
		{
			name:  "batch",
			input: `{"id":"batch-example","status":"completed","request_counts":{"total":2,"completed":1,"failed":1},"errors":{"data":[{"code":"invalid_request","message":"First line\nSecond line"}]},"metadata":{},"cancelled_at":null}`,
			want: "ID: batch-example\nStatus: completed\nRequest counts:\n  Total: 2\n  Completed: 1\n  Failed: 1\n" +
				"Errors:\n  Data:\n    1.\n      Code: invalid_request\n      Message: First line\n        Second line\n" +
				"Metadata: (empty object)\nCancelled at: (null)\n",
		},
		{
			name:  "embedding",
			input: `{"data":[{"object":"embedding","index":0,"embedding":[0.1,-2,3e-5]}],"model":"embedding-example","usage":{"prompt_tokens":3,"total_tokens":3}}`,
			want: "Data:\n  1.\n    Object: embedding\n    Index: 0\n" +
				"    Embedding: (3 numbers; use --format json for full vector)\n" +
				"Model: embedding-example\nUsage:\n  Prompt tokens: 3\n  Total tokens: 3\n",
		},
		{
			name:  "unknown future shape",
			input: `{"new_field":{"data":[[1,true,null],{"unfamiliar":{"value":"kept","flags":[]}}]},"embedding":["not",12,"a vector"],"secret":"synthetic-response-value"}`,
			want: "New field:\n  Data:\n    1.\n      1. 1\n      2. true\n      3. (null)\n\n" +
				"    2.\n      Unfamiliar:\n        Value: kept\n        Flags: (empty list)\n" +
				"Embedding:\n  1. not\n  2. 12\n  3. a vector\nSecret: synthetic-response-value\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := render(t, tt.input); got != tt.want {
				t.Fatalf("output mismatch\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestWriteScalarsAndEmptyContainers(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ input, want string }{
		{`null`, "(null)\n"}, {`false`, "false\n"}, {`true`, "true\n"},
		{`42.125`, "42.125\n"}, {`1e100`, "1e100\n"},
		{`""`, "(empty string)\n"}, {`"hello"`, "hello\n"},
		{`"line one\n\tline two"`, "line one\n\tline two\n"},
		{`{}`, "(empty object)\n"}, {`[]`, "(empty list)\n"},
		{`[{},[],"",null]`, "1. (empty object)\n\n2. (empty list)\n\n3. (empty string)\n4. (null)\n"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got := render(t, tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteOnlySummarizesKnownEncodedFields(t *testing.T) {
	t.Parallel()
	input := `{"b64_json":"aW1hZ2U=","audio":{"data":"YXVkaW8=","transcript":"complete spoken text"},"data":"aW1hZ2U=","content":"YXVkaW8=","audio.data":"literal value","other":{"data":"YXVkaW8="},"unexpected":{"b64_json":"readable unexpected text"}}`
	got := render(t, input)
	want := "B64 JSON: (8 base64 characters; use --format json for full value)\n" +
		"Audio:\n  Data: (8 base64 characters; use --format json for full value)\n  Transcript: complete spoken text\n" +
		"Data: aW1hZ2U=\nContent: YXVkaW8=\n\"audio.data\": literal value\nOther:\n  Data: YXVkaW8=\n" +
		"Unexpected:\n  B64 JSON: readable unexpected text\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteSummarizesOnlyTypedMediaFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, field string }{
		{"speech.audio.delta", "audio"},
		{"image_generation_call", "result"},
		{"response.image_generation_call.partial_image", "partial_image_b64"},
		{"response.audio.delta", "delta"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			const encoded = "c3ludGhldGljLW1lZGlh"
			input := `{"type":"` + tc.kind + `","` + tc.field + `":"` + encoded + `"}`
			got := render(t, input)
			if !strings.Contains(got, tc.kind) || !strings.Contains(got, "--format json") || strings.Contains(got, encoded) {
				t.Fatalf("known media was not summarized: %q", got)
			}
			for _, input := range []string{
				`{"type":"unfamiliar","` + tc.field + `":"` + encoded + `"}`,
				`{"type":"` + tc.kind + `","nested":{"` + tc.field + `":"` + encoded + `"}}`,
			} {
				if got := render(t, input); !strings.Contains(got, encoded) || strings.Contains(got, "base64 characters") {
					t.Errorf("unknown field content was summarized: %q", got)
				}
			}
			input = `{"type":"` + tc.kind + `","` + tc.field + `":"Unexpected readable text!"}`
			if got := render(t, input); !strings.Contains(got, "Unexpected readable text!") || strings.Contains(got, "base64 characters") {
				t.Errorf("invalid encoded media was hidden: %q", got)
			}
		})
	}
}

func TestWritePreservesLargeTextAndEveryArrayItem(t *testing.T) {
	// This is ordinary human-readable content, even though it is much larger
	// than a terminal screen. Size must never determine whether it is retained.
	large := strings.Repeat("Full prose with Unicode 世界 and JSON-like {} [], ", 40000)
	input, err := json.Marshal(map[string]string{"text": large})
	if err != nil {
		t.Fatal(err)
	}
	got := render(t, string(input))
	if got != "Text: "+large+"\n" {
		t.Fatalf("large text changed: got %d bytes, want %d", len(got), len(large)+7)
	}
	got = render(t, "["+strings.Repeat(`"ordinary item",`, 999)+`"last item"]`)
	if strings.Count(got, "ordinary item") != 999 || !strings.HasSuffix(got, "1000. last item\n") {
		t.Fatal("list entries were omitted or capped")
	}
}

func TestWriteLiteralKeysRemainDistinctAndOrdered(t *testing.T) {
	t.Parallel()
	input := `{"file_id":"snake","File ID":"uppercase","file.id":"dot","file":{"id":"nested"},"file id":"space","a_b":"single","a__b":"double","":"empty","line\nbreak":"newline","line\\nbreak":"literal slash","日本語":"unicode"}`
	want := "File ID: snake\n\"File ID\": uppercase\n\"file.id\": dot\nFile:\n  ID: nested\n" +
		"\"file id\": space\nA b: single\n\"a__b\": double\n\"\": empty\n" +
		"\"line\\nbreak\": newline\n\"line\\\\nbreak\": literal slash\n\"日本語\": unicode\n"
	if got := render(t, input); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteEscapesTerminalInjectionInRedirectedOutput(t *testing.T) {
	t.Parallel()
	input := `{"text":"safe\u001b]52;c;synthetic\u0007\u009b2J\rforged\b\u202eevil\u2066nested\u2069\u2028line","bad\u001b[2J\u202e":"value","multiline":"first\nsecond"}`
	got := render(t, input)
	for _, unsafe := range []rune{'\x1b', '\a', '\x9b', '\r', '\b', '\u202e', '\u2066', '\u2069', '\u2028'} {
		if strings.ContainsRune(got, unsafe) {
			t.Errorf("output contains terminal control %U", unsafe)
		}
	}
	for _, want := range []string{`safe\u001b]52;c;synthetic\u0007\u009b2J\rforged\b\u202eevil\u2066nested\u2069\u2028line`, `"bad\x1b[2J\u202e": value`, "Multiline: first\n  second\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q", got, want)
		}
	}
}

func TestTextSupportsStreamingWithoutAddingNewlines(t *testing.T) {
	t.Parallel()
	chunks := []string{"first\n", "\tsecond", "\r\x1b[2J", "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"}
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(Text(chunk))
	}
	want := "first\n\tsecond" + `\r\u001b[2J\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069`
	if got.String() != want {
		t.Fatalf("got %q, want %q", got.String(), want)
	}
}

func TestWritePropagatesWriterErrorsAndShortWrites(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		fail error
		want error
	}{
		{"error", io.ErrClosedPipe, io.ErrClosedPipe},
		{"short write", nil, io.ErrShortWrite},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := &failingWriter{fail: tt.fail}
			err := Write(w, gjson.Parse(`[{"id":"one"},{"id":"two"}]`))
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if w.calls != 1 {
				t.Fatalf("writer received %d calls after failure", w.calls)
			}
		})
	}
}

type failingWriter struct {
	fail  error
	calls int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	// Ignore empty writes, which do not demonstrate a short write.
	if len(p) == 0 {
		return 0, nil
	}
	w.calls++
	return 0, w.fail
}

func render(t *testing.T, input string) string {
	t.Helper()
	var out strings.Builder
	if err := Write(&out, gjson.Parse(input)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
