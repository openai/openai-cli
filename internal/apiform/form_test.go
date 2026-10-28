package apiform

import (
	"bytes"
	"math"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// Define test cases
var tests = map[string]struct {
	value    any
	format   FormFormat
	expected string
}{
	"nil": {
		value:    nil,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n\r\n--xxx--\r\n",
	},
	"string": {
		value:    "hello",
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\nhello\r\n--xxx--\r\n",
	},
	"int": {
		value:    42,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n42\r\n--xxx--\r\n",
	},
	"float": {
		value:    3.14,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n3.14\r\n--xxx--\r\n",
	},
	"float32": {
		value:    float32(0.1),
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n0.1\r\n--xxx--\r\n",
	},
	"negative zero float": {
		value:    math.Copysign(0, -1),
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n-0\r\n--xxx--\r\n",
	},
	"exponent form float": {
		value:    1e3,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n1000\r\n--xxx--\r\n",
	},
	"bool": {
		value:    true,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\ntrue\r\n--xxx--\r\n",
	},
	"empty slice": {
		value:    []string{},
		expected: "\r\n--xxx--\r\n",
	},
	"nil slice": {
		value:    []string(nil),
		expected: "\r\n--xxx--\r\n",
	},
	"slice with dot indices": {
		value:    []string{"a", "b", "c"},
		format:   FormatIndicesDots,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo.0\"\r\n\r\na\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.1\"\r\n\r\nb\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.2\"\r\n\r\nc\r\n--xxx--\r\n",
	},
	"slice with bracket indices": {
		value:    []int{10, 20, 30},
		format:   FormatIndicesBrackets,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo[0]\"\r\n\r\n10\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo[1]\"\r\n\r\n20\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo[2]\"\r\n\r\n30\r\n--xxx--\r\n",
	},
	"slice with repeat": {
		value:    []int{10, 20, 30},
		format:   FormatRepeat,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n10\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n20\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n30\r\n--xxx--\r\n",
	},
	"slice with commas": {
		value:    []int{10, 20, 30},
		format:   FormatComma,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n10,20,30\r\n--xxx--\r\n",
	},
	"float32 slice with commas": {
		value:    []float32{0.1, 1.5},
		format:   FormatComma,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo\"\r\n\r\n0.1,1.5\r\n--xxx--\r\n",
	},
	"empty map": {
		value:    map[string]any{},
		expected: "\r\n--xxx--\r\n",
	},
	"nil map": {
		value:    map[string]any(nil),
		expected: "\r\n--xxx--\r\n",
	},
	"map": {
		value:    map[string]any{"key1": "value1", "key2": "value2"},
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo.key1\"\r\n\r\nvalue1\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.key2\"\r\n\r\nvalue2\r\n--xxx--\r\n",
	},
	"nested_map": {
		value:    map[string]any{"outer": map[string]int{"inner1": 10, "inner2": 20}},
		format:   FormatIndicesDots,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo.outer.inner1\"\r\n\r\n10\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.outer.inner2\"\r\n\r\n20\r\n--xxx--\r\n",
	},
	"nested_map_with_bracket_format": {
		value:    map[string]any{"outer": map[string]int{"inner1": 10, "inner2": 20}},
		format:   FormatBrackets,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo[outer][inner1]\"\r\n\r\n10\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo[outer][inner2]\"\r\n\r\n20\r\n--xxx--\r\n",
	},
	"mixed_map": {
		value:    map[string]any{"name": "John", "ages": []int{25, 30, 35}},
		format:   FormatIndicesDots,
		expected: "--xxx\r\nContent-Disposition: form-data; name=\"foo.ages.0\"\r\n\r\n25\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.ages.1\"\r\n\r\n30\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.ages.2\"\r\n\r\n35\r\n--xxx\r\nContent-Disposition: form-data; name=\"foo.name\"\r\n\r\nJohn\r\n--xxx--\r\n",
	},
}

func TestEncode(t *testing.T) {
	t.Parallel()

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			buf := bytes.NewBuffer(nil)
			writer := multipart.NewWriter(buf)
			writer.SetBoundary("xxx")

			form := map[string]any{"foo": test.value}
			err := MarshalWithSettings(form, writer, test.format)
			if err != nil {
				t.Errorf("serialization of %v failed with error %v", test.value, err)
			}
			err = writer.Close()
			if err != nil {
				t.Errorf("serialization of %v failed with error %v", test.value, err)
			}
			result := buf.String()
			if result != test.expected {
				t.Errorf("expected %+#v to serialize to:\n\t%q\nbut got:\n\t%q", test.value, test.expected, result)
			}
		})
	}
}

func TestMarshalRejectsNonFiniteFloats(t *testing.T) {
	t.Parallel()

	inf32 := float32(math.Inf(1))
	nan32 := float32(math.NaN())
	infPtr := math.Inf(1)

	tests := map[string]struct {
		value  any
		format FormFormat
	}{
		"float64 +Inf":      {value: math.Inf(1)},
		"float64 -Inf":      {value: math.Inf(-1)},
		"float64 NaN":       {value: math.NaN()},
		"float32 +Inf":      {value: inf32},
		"float32 NaN":       {value: nan32},
		"pointer to +Inf":   {value: &infPtr},
		"nested map +Inf":   {value: map[string]any{"nested": math.Inf(1)}},
		"comma slice +Inf":  {value: []float64{1.5, math.Inf(1)}, format: FormatComma},
		"comma slice NaN":   {value: []float32{nan32}, format: FormatComma},
		"repeat slice -Inf": {value: []float64{math.Inf(-1)}, format: FormatRepeat},
		"indices slice NaN": {value: []float64{math.NaN()}, format: FormatIndicesDots},
		"piped YAML .inf":   {value: pipedYAMLBody(t, "temperature: .inf\n")},
		"piped YAML -.inf":  {value: pipedYAMLBody(t, "temperature: -.inf\n")},
		"piped YAML .nan":   {value: pipedYAMLBody(t, "temperature: .nan\n")},
		"piped YAML .Inf":   {value: pipedYAMLBody(t, "temperature: .Inf\n")},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			buf := bytes.NewBuffer(nil)
			writer := multipart.NewWriter(buf)
			writer.SetBoundary("xxx")

			form := map[string]any{"foo": test.value}
			err := MarshalWithSettings(form, writer, test.format)
			if err == nil {
				t.Fatalf("expected an error encoding %v, got body %q", test.value, buf.String())
			}
			if !strings.Contains(err.Error(), "unsupported value") {
				t.Errorf("expected an unsupported value error, got %v", err)
			}
		})
	}
}

// pipedYAMLBody simulates the stdin/YAML route into the encoder: a piped
// request body is parsed with the YAML decoder into a generic map before
// Marshal runs, bypassing typed flag parsing entirely.
func pipedYAMLBody(t *testing.T, source string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := yaml.Unmarshal([]byte(source), &body); err != nil {
		t.Fatalf("failed to parse test YAML %q: %v", source, err)
	}
	return body
}
