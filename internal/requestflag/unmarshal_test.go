package requestflag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestUnmarshalYAMLOrJSONExponentNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  any
	}{
		{
			name:  "Python json.dumps small float",
			input: `{"top_p": 1e-05}`,
			want:  map[string]any{"top_p": 1e-05},
		},
		{
			name:  "uppercase exponent with explicit sign",
			input: `{"n":2E+3}`,
			want:  map[string]any{"n": float64(2000)},
		},
		{
			name:  "negative and zero mantissas",
			input: `[-5e-7,0e0,-0E-0]`,
			want:  []any{-5e-7, float64(0), float64(0)},
		},
		{
			name:  "top-level number",
			input: `1e-06`,
			want:  1e-06,
		},
		{
			name:  "nested schema keeps fractional exponents",
			input: `{"schema":{"minimum":1e-07,"maximum":1.5e-3}}`,
			want:  map[string]any{"schema": map[string]any{"minimum": 1e-07, "maximum": 1.5e-3}},
		},
		{
			name:  "strings and keys that look like numbers are unchanged",
			input: `{"1e5":"1e-05","quoted":"a\"1e5\"","escaped":["\\",1e2]}`,
			want: map[string]any{
				"1e5":     "1e-05",
				"quoted":  `a"1e5"`,
				"escaped": []any{`\`, float64(100)},
			},
		},
		{
			name:  "other numbers keep their existing types",
			input: `{"unsigned":42,"signed":-42,"fraction":1.5}`,
			want:  map[string]any{"unsigned": uint64(42), "signed": int64(-42), "fraction": 1.5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got any
			require.NoError(t, UnmarshalYAMLOrJSON([]byte(tt.input), &got))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUnmarshalYAMLOrJSONPreservesDecoderRules(t *testing.T) {
	t.Parallel()

	// Only valid JSON is rewritten, so YAML and malformed input reach the
	// decoder byte for byte.
	for _, input := range []string{"top_p: 1e-05\n", "{top_p: 1e-05}", `{"top_p": 1e-05`} {
		assert.Equal(t, input, string(addJSONExponentFractions([]byte(input))))
	}

	var got any
	assert.Error(t, UnmarshalYAMLOrJSON([]byte(`{"k":1e-05,"k":2}`), &got), "duplicate keys are still rejected")
}

func TestJSONExponentNumbersInFlagValues(t *testing.T) {
	t.Parallel()

	t.Run("map flag", func(t *testing.T) {
		t.Parallel()

		cv := &cliValue[map[string]any]{}
		require.NoError(t, cv.Set(`{"format":{"type":"json_schema","schema":{"type":"number","minimum":1e-07}}}`))
		schema := cv.Get().(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)
		assert.Equal(t, 1e-07, schema["minimum"])
	})

	t.Run("repeated object flag", func(t *testing.T) {
		t.Parallel()

		cv := &cliValue[[]map[string]any]{}
		require.NoError(t, cv.Set(`{"type":"function","parameters":{"multipleOf":2E-3}}`))
		parameters := cv.Get().([]map[string]any)[0]["parameters"].(map[string]any)
		assert.Equal(t, 2e-3, parameters["multipleOf"])
	})

	t.Run("piped value reformatted for a flag", func(t *testing.T) {
		t.Parallel()

		// formatForFlagSet JSON-encodes piped maps, and encoding/json writes
		// float64(1e-07) as 1e-7 before the flag parses it again.
		flag := &Flag[map[string]any]{Name: "filter", QueryPath: "filter"}
		require.NoError(t, flag.PreParse())
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		require.NoError(t, ApplyStdinDataToFlags(cmd, map[string]any{"filter": map[string]any{"min": 1e-07}}))
		assert.Equal(t, map[string]any{"min": 1e-07}, flag.Get())
	})
}
