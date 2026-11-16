package output

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestFormatJSON(t *testing.T) {
	t.Parallel()

	t.Run("RawWithTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123","name":"test"}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "raw", Transform: "id"}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		require.Equal(t, `"abc123"`+"\n", string(formatted))
	})

	t.Run("RawWithoutTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123","name":"test"}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "raw"}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		require.Equal(t, `{"id":"abc123","name":"test"}`+"\n", string(formatted))
	})

	t.Run("RawWithNestedTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"data":{"items":[1,2,3]}}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "raw", Transform: "data.items"}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		require.Equal(t, "[1,2,3]\n", string(formatted))
	})

	t.Run("RawWithNonexistentTransform", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123"}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "raw", Transform: "missing"}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		// Transform path doesn't exist, so original result is returned
		require.Equal(t, `{"id":"abc123"}`+"\n", string(formatted))
	})

	t.Run("RawOutputString", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"id":"abc123","name":"test"}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "json", Transform: "id", RawOutput: true}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		require.Equal(t, "abc123\n", string(formatted))
	})

	t.Run("RawOutputNonString", func(t *testing.T) {
		t.Parallel()

		// --raw-output has no effect on non-string values
		res := gjson.Parse(`{"count":42}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "raw", Transform: "count", RawOutput: true}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		require.Equal(t, "42\n", string(formatted))
	})

	t.Run("RawOutputObject", func(t *testing.T) {
		t.Parallel()

		// --raw-output has no effect on objects
		res := gjson.Parse(`{"nested":{"a":1}}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "raw", Transform: "nested", RawOutput: true}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)
		require.Equal(t, `{"a":1}`+"\n", string(formatted))
	})

	t.Run("PrettyEscapesDecodedTerminalControlSequences", func(t *testing.T) {
		t.Parallel()

		res := gjson.Parse(`{"message":"\u001b]52;c;ZGF0YQ==\u0007"}`)
		formatted, err := FormatJSON(res, FormatOptions{Format: "pretty"}, os.Stdout, term.IsTerminal(os.Stdout.Fd()))
		require.NoError(t, err)

		out := string(formatted)
		require.NotContains(t, out, "\x1b]52")
		require.NotContains(t, out, "\a")
		require.Contains(t, out, `\u001b]52;c;ZGF0YQ==\u0007`)
	})
}

func TestResolvedOutputFormat(t *testing.T) {
	for _, tc := range []struct {
		format, transform string
		raw               bool
		want              string
	}{
		{"", "", false, "text"}, {"AuTo", "", false, "text"},
		{"auto", "id", false, "json"}, {"auto", "", true, "json"},
		{"TEXT", "id", false, "text"}, {"JSONL", "", false, "jsonl"},
		{"raw", "id", true, "raw"}, {"unknown", "", false, "unknown"},
	} {
		require.Equal(t, tc.want, ResolveFormat(FormatOptions{Format: tc.format, Transform: tc.transform, RawOutput: tc.raw}))
	}
}

func TestFormatJSONFinalDestinationPolicy(t *testing.T) {
	for _, test := range []struct {
		name, force, noColor string
		terminal, wantColor  bool
	}{
		{name: "terminal", terminal: true, wantColor: true},
		{name: "terminal forced", terminal: true, force: "1", wantColor: true},
		{name: "terminal disabled", terminal: true, force: "0"},
		{name: "NO_COLOR wins", terminal: true, force: "1", noColor: "1"},
		{name: "pipe forced", force: "1"},
		{name: "pipe", terminal: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FORCE_COLOR", test.force)
			t.Setenv("NO_COLOR", test.noColor)
			for _, format := range []string{"json", "jsonl"} {
				data, err := FormatJSON(gjson.Parse(`{"value":"synthetic"}`), FormatOptions{Format: format}, io.Discard, test.terminal)
				require.NoError(t, err)
				require.Equal(t, test.wantColor, bytes.Contains(data, []byte("\x1b[")), format)
			}
			data, err := FormatJSON(gjson.Parse(`{"value":"\u001b[31m\u0007"}`), FormatOptions{Format: "auto", Transform: "value", RawOutput: true}, io.Discard, test.terminal)
			require.NoError(t, err)
			if test.terminal {
				require.Equal(t, "\\u001b[31m\\u0007\n", string(data))
			} else {
				require.Equal(t, "\x1b[31m\a\n", string(data))
			}
		})
	}
}

func TestFormatJSONPreservesFormatBytesAndErrors(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	t.Setenv("NO_COLOR", "1")
	for _, test := range []struct{ format, want string }{
		{"auto", "Value: synthetic\n"},
		{"text", "Value: synthetic\n"},
		{"JSON", "{\n  \"value\": \"synthetic\"\n}\n"},
		{"jsonl", "{\"value\":\"synthetic\"}\n"},
		{"raw", "{\"value\":\"synthetic\"}\n"},
		{"yaml", "value: synthetic\n"},
	} {
		t.Run(test.format, func(t *testing.T) {
			data, err := FormatJSON(gjson.Parse(`{"value":"synthetic"}`), FormatOptions{Format: test.format}, io.Discard, false)
			require.NoError(t, err)
			require.Equal(t, test.want, string(data))
		})
	}
	data, err := FormatJSON(gjson.Parse(`{}`), FormatOptions{Format: "invalid"}, io.Discard, false)
	require.Nil(t, data)
	require.EqualError(t, err, "Invalid format: invalid, valid formats are: auto, text, explore, json, jsonl, pretty, raw, yaml")
}
