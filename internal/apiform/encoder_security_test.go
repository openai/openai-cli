package apiform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestMarshalRejectsInvalidMIMEHeaderControls(t *testing.T) {
	t.Parallel()

	type controlCase struct {
		name  string
		value string
	}
	controls := make([]controlCase, 0, 32)
	for value := byte(0); value < ' '; value++ {
		if value == '\t' {
			continue
		}
		name := fmt.Sprintf("0x%02x", value)
		switch value {
		case '\x00':
			name = "NUL"
		case '\n':
			name = "LF"
		case '\r':
			name = "CR"
		}
		controls = append(controls, controlCase{name: name, value: string(value)})
	}
	controls = append(controls, controlCase{name: "DEL", value: "\x7f"})

	for _, control := range controls {
		t.Run(control.name, func(t *testing.T) {
			t.Parallel()

			injected := "before" + control.value + "X-Injected: value"
			cases := []struct {
				name   string
				value  any
				format FormFormat
			}{
				{
					name:  "scalar field name",
					value: map[string]any{injected: "value"},
				},
				{
					name:  "nil field name",
					value: map[string]any{injected: nil},
				},
				{
					name: "file field name",
					value: map[string]any{injected: multipartSecurityFile{
						Reader:      strings.NewReader("contents"),
						filename:    "valid.txt",
						contentType: "text/plain",
					}},
				},
				{
					name:  "nested dotted field name",
					value: map[string]any{"outer": map[string]any{injected: "value"}},
				},
				{
					name:   "nested bracketed field name",
					value:  map[string]any{"outer": map[string]any{injected: "value"}},
					format: FormatBrackets,
				},
				{
					name:   "comma-separated field name",
					value:  map[string]any{injected: []string{"first", "second"}},
					format: FormatComma,
				},
				{
					name: "filename",
					value: map[string]any{"file": multipartSecurityFile{
						Reader:      strings.NewReader("contents"),
						filename:    injected,
						contentType: "text/plain",
					}},
				},
				{
					name: "reader Name filename",
					value: map[string]any{"file": multipartSecurityNamedReader{
						Reader: strings.NewReader("contents"),
						name:   "/tmp/" + injected,
					}},
				},
				{
					name: "content type",
					value: map[string]any{"file": multipartSecurityFile{
						Reader:      strings.NewReader("contents"),
						filename:    "valid.txt",
						contentType: "text/plain" + control.value + "X-Injected: value",
					}},
				},
			}

			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()

					var output bytes.Buffer
					writer := multipart.NewWriter(&output)
					if err := MarshalWithSettings(test.value, writer, test.format); err == nil {
						t.Fatalf("encoding unexpectedly accepted a %s control in %s", control.name, test.name)
					}
					if output.Len() != 0 {
						t.Errorf("invalid part wrote %d bytes before being rejected", output.Len())
					}
				})
			}
		})
	}
}

func TestMarshalRejectsNestedJSONAndYAMLFieldNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		input  string
		decode func([]byte, any) error
	}{
		{
			name:   "JSON",
			input:  `{"outer":{"bad\r\nX-Injected: value":"contents"}}`,
			decode: json.Unmarshal,
		},
		{
			name:   "YAML",
			input:  "outer:\n  \"bad\\r\\nX-Injected: value\": contents\n",
			decode: yaml.Unmarshal,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var value map[string]any
			if err := test.decode([]byte(test.input), &value); err != nil {
				t.Fatalf("decode input: %v", err)
			}

			for _, format := range []FormFormat{FormatRepeat, FormatBrackets} {
				t.Run(fmt.Sprintf("format_%d", format), func(t *testing.T) {
					t.Parallel()

					var output bytes.Buffer
					if err := MarshalWithSettings(value, multipart.NewWriter(&output), format); err == nil {
						t.Fatal("encoding unexpectedly accepted a decoded nested CRLF field name")
					}
					if output.Len() != 0 {
						t.Errorf("invalid nested field wrote %d bytes before being rejected", output.Len())
					}
				})
			}
		})
	}
}

func TestMarshalPreservesValidMIMEHeaderValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		field       string
		filename    string
		contentType string
	}{
		{
			name:        "Unicode quotes and backslashes",
			field:       "résumé \\\"field\\\"",
			filename:    "写真 \\\"draft\\\".txt",
			contentType: "application/vnd.example+json; charset=\"utf-8\"",
		},
		{
			name:        "horizontal tabs",
			field:       "field\tname",
			filename:    "upload\tname.txt",
			contentType: "text/plain\t; charset=utf-8",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			const contents = "streamed payload"
			var output bytes.Buffer
			writer := multipart.NewWriter(&output)
			err := Marshal(map[string]any{test.field: multipartSecurityFile{
				Reader:      strings.NewReader(contents),
				filename:    test.filename,
				contentType: test.contentType,
			}}, writer)
			if err != nil {
				t.Fatalf("encode valid multipart values: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close multipart writer: %v", err)
			}

			reader := multipart.NewReader(&output, writer.Boundary())
			part, err := reader.NextPart()
			if err != nil {
				t.Fatalf("read multipart part: %v", err)
			}
			if got := part.FormName(); got != test.field {
				t.Errorf("form name = %q, want %q", got, test.field)
			}
			wantDisposition := fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
				escapeQuotes(test.field), escapeQuotes(test.filename))
			if got := part.Header.Get("Content-Disposition"); got != wantDisposition {
				t.Errorf("content disposition = %q, want %q", got, wantDisposition)
			}
			if got := part.Header.Get("Content-Type"); got != strings.TrimSpace(test.contentType) {
				t.Errorf("content type = %q, want %q", got, strings.TrimSpace(test.contentType))
			}
			got, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read multipart payload: %v", err)
			}
			if string(got) != contents {
				t.Errorf("multipart payload = %q, want %q", got, contents)
			}
		})
	}
}

type multipartSecurityFile struct {
	io.Reader
	filename    string
	contentType string
}

func (f multipartSecurityFile) Filename() string    { return f.filename }
func (f multipartSecurityFile) ContentType() string { return f.contentType }

type multipartSecurityNamedReader struct {
	io.Reader
	name string
}

func (r multipartSecurityNamedReader) Name() string { return r.name }
