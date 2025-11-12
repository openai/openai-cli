package requestflag

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestDateValueParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "ISO format",
			input:   "2023-05-15",
			want:    "2023-05-15",
			wantErr: false,
		},
		{
			name:    "US format",
			input:   "05/15/2023",
			want:    "2023-05-15",
			wantErr: false,
		},
		{
			name:    "Short month format",
			input:   "May 15, 2023",
			want:    "2023-05-15",
			wantErr: false,
		},
		{
			name:    "Long month format",
			input:   "January 15, 2023",
			want:    "2023-01-15",
			wantErr: false,
		},
		{
			name:    "British format",
			input:   "15-Jan-2023",
			want:    "2023-01-15",
			wantErr: false,
		},
		{
			name:    "Invalid format",
			input:   "not a date",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var d DateValue
			err := d.Parse(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, d.String())
			}
		})
	}
}

func TestDateTimeValueParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "RFC3339",
			input:   "2023-05-15T14:30:45Z",
			wantErr: false,
		},
		{
			name:    "ISO with timezone",
			input:   "2023-05-15T14:30:45+02:00",
			wantErr: false,
		},
		{
			name:    "ISO without timezone",
			input:   "2023-05-15T14:30:45",
			wantErr: false,
		},
		{
			name:    "Space separated",
			input:   "2023-05-15 14:30:45",
			wantErr: false,
		},
		{
			name:    "RFC1123",
			input:   "Mon, 15 May 2023 14:30:45 GMT",
			wantErr: false,
		},
		{
			name:    "RFC822",
			input:   "15 May 23 14:30 GMT",
			wantErr: false,
		},
		{
			name:    "ANSIC",
			input:   "Mon Jan 2 15:04:05 2006",
			wantErr: false,
		},
		{
			name:    "Invalid format",
			input:   "not a datetime",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var d DateTimeValue
			err := d.Parse(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Parse the string back to ensure it's valid RFC3339
				_, parseErr := time.Parse(time.RFC3339, d.String())
				assert.NoError(t, parseErr)
			}
		})
	}
}

func TestTimeValueParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "24-hour format",
			input:   "14:30:45",
			want:    "14:30:45",
			wantErr: false,
		},
		{
			name:    "12-hour format with seconds",
			input:   "2:30:45PM",
			want:    "14:30:45",
			wantErr: false,
		},
		{
			name:    "12-hour format without seconds",
			input:   "2:30 PM",
			want:    "14:30:00",
			wantErr: false,
		},
		{
			name:    "24-hour without seconds",
			input:   "14:30",
			want:    "14:30:00",
			wantErr: false,
		},
		{
			name:    "Kitchen format",
			input:   "2:30PM",
			want:    "14:30:00",
			wantErr: false,
		},
		{
			name:    "Invalid format",
			input:   "not a time",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tv TimeValue
			err := tv.Parse(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, tv.String())
			}
		})
	}
}

func TestRequestParams(t *testing.T) {
	t.Parallel()

	t.Run("map body type", func(t *testing.T) {
		t.Parallel()

		// Create a mock command with flags
		cmd := &cli.Command{
			Name: "test",
		}

		// Create string flag with body path
		stringFlag := &Flag[string]{
			Name:       "string-flag",
			Default:    "default-string",
			BodyPath:   "string_field",
			value:      &cliValue[string]{value: "test-value"},
			hasBeenSet: true,
		}

		// Create int flag with header path
		intFlag := &Flag[int64]{
			Name:       "int-flag",
			Default:    42,
			HeaderPath: "X-Int-Value",
			value:      &cliValue[int64]{value: 99},
			hasBeenSet: true,
		}

		// Create bool flag with query path
		boolFlag := &Flag[bool]{
			Name:       "bool-flag",
			Default:    false,
			QueryPath:  "include_details",
			value:      &cliValue[bool]{value: true},
			hasBeenSet: true,
		}

		// Create date flag with multiple paths
		dateFlag := &Flag[DateValue]{
			Name:       "date-flag",
			Default:    DateValue("2023-01-01"),
			BodyPath:   "effective_date",
			HeaderPath: "X-Effective-Date",
			QueryPath:  "as_of_date",
			value:      &cliValue[DateValue]{value: DateValue("2023-05-15")},
			hasBeenSet: true,
		}

		// Create flag with no path
		noPathFlag := &Flag[string]{
			Name:       "no-path-flag",
			Default:    "no-path",
			value:      &cliValue[string]{value: "no-path-value"},
			hasBeenSet: true,
		}

		// Create unset flag
		unsetFlag := &Flag[string]{
			Name:       "unset-flag",
			Default:    "unset",
			BodyPath:   "should_not_appear",
			value:      &cliValue[string]{value: "unset-value"},
			hasBeenSet: false,
		}

		cmd.Flags = []cli.Flag{stringFlag, intFlag, boolFlag, dateFlag, noPathFlag, unsetFlag}

		// Test the RequestParams function
		contents := ExtractRequestContents(cmd)

		// Verify query parameters
		assert.Equal(t, true, contents.Queries["include_details"])
		assert.Equal(t, DateValue("2023-05-15"), contents.Queries["as_of_date"])
		assert.Len(t, contents.Queries, 2)

		// Verify headers
		assert.Equal(t, int64(99), contents.Headers["X-Int-Value"])
		assert.Equal(t, DateValue("2023-05-15"), contents.Headers["X-Effective-Date"])
		assert.Len(t, contents.Headers, 2)

		// Verify body
		bodyMap, ok := contents.Body.(map[string]any)
		assert.True(t, ok, "Expected body to be map[string]any, got %T", contents.Body)
		assert.Equal(t, "test-value", bodyMap["string_field"])
		assert.Equal(t, DateValue("2023-05-15"), bodyMap["effective_date"])
		assert.Len(t, bodyMap, 2)

		// Verify the unset flag didn't make it into the maps
		assert.NotContains(t, contents.Body, "should_not_appear")
	})

	t.Run("non-map body type", func(t *testing.T) {
		t.Parallel()

		// Create a mock command with flags
		cmd := &cli.Command{
			Name: "test",
			Flags: []cli.Flag{
				&Flag[int64]{
					Name:     "int-body-flag",
					Default:  0,
					BodyRoot: true,
				},
			},
		}
		cmd.Set("int-body-flag", "42")

		contents := ExtractRequestContents(cmd)
		intBody, ok := contents.Body.(int64)
		assert.True(t, ok, "Expected body to be int64, got %T", contents.Body)
		assert.Equal(t, int64(42), intBody)
	})
}

func TestFlagSet(t *testing.T) {
	t.Parallel()

	strFlag := &Flag[string]{
		Name:    "string-flag",
		Default: "default-string",
	}

	superstitiousIntFlag := &Flag[int64]{
		Name:    "int-flag",
		Default: 42,
		Validator: func(val int64) error {
			if val == 13 {
				return fmt.Errorf("Unlucky number!")
			}
			return nil
		},
	}

	boolFlag := &Flag[bool]{
		Name:    "bool-flag",
		Default: false,
	}

	// Test initialization and setting
	t.Run("PreParse initialization", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, strFlag.PreParse())
		assert.True(t, strFlag.applied)
		assert.Equal(t, "default-string", strFlag.Get())
	})

	t.Run("Set string flag", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, strFlag.Set("string-flag", "new-value"))
		assert.Equal(t, "new-value", strFlag.Get())
		assert.True(t, strFlag.IsSet())
	})

	t.Run("Set int flag with valid value", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, superstitiousIntFlag.Set("int-flag", "100"))
		assert.Equal(t, int64(100), superstitiousIntFlag.Get())
		assert.True(t, superstitiousIntFlag.IsSet())
	})

	t.Run("Set int flag with invalid value", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, superstitiousIntFlag.Set("int-flag", "not-an-int"))
	})

	t.Run("Set int flag with validator failing", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, superstitiousIntFlag.Set("int-flag", "13"))
	})

	t.Run("Set bool flag", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, boolFlag.Set("bool-flag", "true"))
		assert.Equal(t, true, boolFlag.Get())
		assert.True(t, boolFlag.IsSet())
	})

	t.Run("Set slice flag with multiple values", func(t *testing.T) {
		t.Parallel()

		sliceFlag := &Flag[[]int64]{
			Name:    "slice-flag",
			Default: []int64{},
		}

		// Initialize the flag
		assert.NoError(t, sliceFlag.PreParse())

		// First set
		assert.NoError(t, sliceFlag.Set("slice-flag", "10"))

		// Subsequent setting should append, not replace
		assert.NoError(t, sliceFlag.Set("slice-flag", "20"))
		assert.NoError(t, sliceFlag.Set("slice-flag", "30"))

		// Verify that we have both values in the slice
		result := sliceFlag.Get()
		assert.Equal(t, []int64{10, 20, 30}, result)
		assert.True(t, sliceFlag.IsSet())
	})

	t.Run("Set slice flag with a nonempty default", func(t *testing.T) {
		t.Parallel()

		sliceFlag := &Flag[[]int64]{
			Name:    "slice-flag",
			Default: []int64{99, 100},
		}

		assert.NoError(t, sliceFlag.PreParse())
		assert.NoError(t, sliceFlag.Set("slice-flag", "10"))
		assert.NoError(t, sliceFlag.Set("slice-flag", "20"))
		assert.NoError(t, sliceFlag.Set("slice-flag", "30"))

		// Verify that we have clobbered the default value instead of appending
		// to it.
		result := sliceFlag.Get()
		assert.Equal(t, []int64{10, 20, 30}, result)
		assert.True(t, sliceFlag.IsSet())
	})
}

func TestParseTimeWithFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		formats  []string
		wantTime time.Time
		wantErr  bool
	}{
		{
			name:     "RFC3339 format",
			input:    "2023-05-15T14:30:45Z",
			formats:  []string{time.RFC3339},
			wantTime: time.Date(2023, 5, 15, 14, 30, 45, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "Multiple formats - first matches",
			input:    "2023-05-15",
			formats:  []string{"2006-01-02", time.RFC3339},
			wantTime: time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "Multiple formats - second matches",
			input:    "15/05/2023",
			formats:  []string{"2006-01-02", "02/01/2006"},
			wantTime: time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "No matching format",
			input:    "not a date",
			formats:  []string{"2006-01-02", time.RFC3339},
			wantTime: time.Time{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTimeWithFormats(tt.input, tt.formats)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.True(t, tt.wantTime.Equal(got), "Expected %v, got %v", tt.wantTime, got)
			}
		})
	}
}

func TestYamlHandling(t *testing.T) {
	t.Parallel()

	// Test with any value
	t.Run("Parse YAML to any", func(t *testing.T) {
		t.Parallel()

		cv := &cliValue[any]{}
		err := cv.Set("name: test\nvalue: 42\n")
		assert.NoError(t, err)

		// The value should be a map
		val, ok := cv.Get().(map[string]any)
		assert.True(t, ok, "Expected map[string]any, got %T", cv.Get())

		if ok {
			assert.Equal(t, "test", val["name"])
			assert.Equal(t, uint64(42), val["value"])
		}

		// The string representation should be valid YAML
		strVal := cv.String()
		var parsed map[string]any
		err = yaml.Unmarshal([]byte(strVal), &parsed)
		assert.NoError(t, err)
		assert.Equal(t, "test", parsed["name"])
		assert.Equal(t, uint64(42), parsed["value"])
	})

	// Test with array
	t.Run("Parse YAML array", func(t *testing.T) {
		t.Parallel()

		cv := &cliValue[any]{}
		err := cv.Set("- item1\n- item2\n- item3\n")
		assert.NoError(t, err)

		// The value should be a slice
		val, ok := cv.Get().([]any)
		assert.True(t, ok, "Expected []any, got %T", cv.Get())

		if ok {
			assert.Len(t, val, 3)
			assert.Equal(t, "item1", val[0])
			assert.Equal(t, "item2", val[1])
			assert.Equal(t, "item3", val[2])
		}
	})

	t.Run("Parse @file.txt as YAML", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[any]{
			Name:    "file-flag",
			Default: nil,
		}
		assert.NoError(t, flag.PreParse())
		assert.NoError(t, flag.Set("file-flag", "@file.txt"))

		val := flag.Get()
		assert.Equal(t, "@file.txt", val)
	})

	t.Run("Parse @file.txt list as YAML", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[[]any]{
			Name:    "file-flag",
			Default: nil,
		}
		assert.NoError(t, flag.PreParse())
		assert.NoError(t, flag.Set("file-flag", "@file1.txt"))
		assert.NoError(t, flag.Set("file-flag", "@file2.txt"))

		val := flag.Get()
		assert.Equal(t, []any{"@file1.txt", "@file2.txt"}, val)
	})

	t.Run("Parse identifiers as YAML", func(t *testing.T) {
		t.Parallel()

		tests := []string{
			"hello",
			"e4e355fa-b03b-4c57-a73d-25c9733eec79",
			"foo_bar",
			"Color.Red",
			"aGVsbG8=",
		}
		for _, test := range tests {
			flag := &Flag[any]{
				Name:    "flag",
				Default: nil,
			}
			assert.NoError(t, flag.PreParse())
			assert.NoError(t, flag.Set("flag", test))

			val := flag.Get()
			assert.Equal(t, test, val)
		}

		for _, test := range tests {
			flag := &Flag[[]any]{
				Name:    "identifier",
				Default: nil,
			}
			assert.NoError(t, flag.PreParse())
			assert.NoError(t, flag.Set("identifier", test))
			assert.NoError(t, flag.Set("identifier", test))

			val := flag.Get()
			assert.Equal(t, []any{test, test}, val)
		}
	})

	// Test with invalid YAML
	t.Run("Parse invalid YAML", func(t *testing.T) {
		t.Parallel()

		invalidYaml := `[not closed`
		cv := &cliValue[any]{}
		err := cv.Set(invalidYaml)
		assert.Error(t, err)
	})
}

// TestNullLiteralHandling pins how each Flag[T] type handles the literal value "null"
// when passed via the CLI. Pointer-typed flags serialize nil as JSON null, which is how
// nullable body fields (`anyOf: [T, null]` / `{nullable: true}`) let users clear a field
// via `--foo null`. Non-pointer primitive flags treat "null" as a raw value — these are
// non-nullable schemas where explicit null has no API semantics anyway.
func TestNullLiteralHandling(t *testing.T) {
	t.Parallel()

	assertJSONBody := func(t *testing.T, value any, expected string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"foo": value})
		assert.NoError(t, err)
		assert.JSONEq(t, expected, string(body))
	}

	t.Run("Flag[any] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[any]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[string] null is the raw string \"null\"", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[string]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":"null"}`)
	})

	t.Run("Flag[int64] null errors", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[int64]{}
		assert.Error(t, cv.Set("null"))
	})

	t.Run("Flag[*string] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*string]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*string] value sends the string", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*string]{}
		assert.NoError(t, cv.Set("1.1"))
		assertJSONBody(t, cv.Get(), `{"foo":"1.1"}`)
	})

	t.Run("Flag[*int64] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*int64]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*int64] value sends the integer", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*int64]{}
		assert.NoError(t, cv.Set("42"))
		assertJSONBody(t, cv.Get(), `{"foo":42}`)
	})

	t.Run("Flag[*int64] invalid value errors", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*int64]{}
		assert.Error(t, cv.Set("not-an-int"))
	})

	t.Run("Flag[*bool] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*bool]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*bool] value sends the boolean", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*bool]{}
		assert.NoError(t, cv.Set("true"))
		assertJSONBody(t, cv.Get(), `{"foo":true}`)
	})

	t.Run("Flag[*float64] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*float64]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*float64] value sends the float", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*float64]{}
		assert.NoError(t, cv.Set("1.5"))
		assertJSONBody(t, cv.Get(), `{"foo":1.5}`)
	})

	t.Run("Flag[*float64] invalid value errors", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*float64]{}
		assert.Error(t, cv.Set("not-a-float"))
	})

	t.Run("Flag[*DateValue] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*DateValue]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*DateValue] value sends the date", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*DateValue]{}
		assert.NoError(t, cv.Set("2023-05-15"))
		assertJSONBody(t, cv.Get(), `{"foo":"2023-05-15"}`)
	})

	t.Run("Flag[*DateValue] invalid value errors", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*DateValue]{}
		assert.Error(t, cv.Set("not-a-date"))
	})

	t.Run("Flag[*DateTimeValue] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*DateTimeValue]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*DateTimeValue] value sends the datetime", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*DateTimeValue]{}
		assert.NoError(t, cv.Set("2023-05-15T14:30:45Z"))
		assertJSONBody(t, cv.Get(), `{"foo":"2023-05-15T14:30:45Z"}`)
	})

	t.Run("Flag[*DateTimeValue] invalid value errors", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*DateTimeValue]{}
		assert.Error(t, cv.Set("not-a-datetime"))
	})

	t.Run("Flag[*TimeValue] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*TimeValue]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})

	t.Run("Flag[*TimeValue] value sends the time", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*TimeValue]{}
		assert.NoError(t, cv.Set("14:30:45"))
		assertJSONBody(t, cv.Get(), `{"foo":"14:30:45"}`)
	})

	t.Run("Flag[*TimeValue] invalid value errors", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[*TimeValue]{}
		assert.Error(t, cv.Set("not-a-time"))
	})

	// Nullable maps don't need pointer wrapping — a nil map already marshals as JSON null.
	t.Run("Flag[map[string]any] null sends JSON null", func(t *testing.T) {
		t.Parallel()
		cv := &cliValue[map[string]any]{}
		assert.NoError(t, cv.Set("null"))
		assertJSONBody(t, cv.Get(), `{"foo":null}`)
	})
}

func TestFlagTypeNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flag     cli.DocGenerationFlag
		expected string
	}{
		{"string", &Flag[string]{}, "string"},
		{"int64", &Flag[int64]{}, "int"},
		{"float64", &Flag[float64]{}, "float"},
		{"bool", &Flag[bool]{}, "boolean"},
		{"string slice", &Flag[[]string]{}, "string"},
		{"date", &Flag[DateValue]{}, "date"},
		{"datetime", &Flag[DateTimeValue]{}, "datetime"},
		{"time", &Flag[TimeValue]{}, "time"},
		{"date slice", &Flag[[]DateValue]{}, "date"},
		{"datetime slice", &Flag[[]DateTimeValue]{}, "datetime"},
		{"time slice", &Flag[[]TimeValue]{}, "time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			typeName := tt.flag.TypeName()
			assert.Equal(t, tt.expected, typeName, "Expected type name %q, got %q", tt.expected, typeName)
		})
	}
}

// TestInnerFlagDispatchOnUntypedFlag pins inner-flag behavior for `Flag[any]`,
// which is the codegen output for nullable complex schemas (`anyOf: [T, null]`
// or `{nullable: true}`). The untyped-nil zero value carries no reflect.Kind,
// so SetInnerField has nowhere to dispatch the assignment — without explicit
// help the inner-field value silently drops.
func TestInnerFlagDispatchOnUntypedFlag(t *testing.T) {
	t.Parallel()

	t.Run("nullable array of objects appends element from inner flag", func(t *testing.T) {
		t.Parallel()
		outer := &Flag[any]{Name: "mcp-server"}
		assert.NoError(t, outer.PreParse())

		nameFlag := &InnerFlag[string]{
			Name: "mcp-server.name", InnerField: "name",
			OuterFlag: outer, OuterIsArrayOfObjects: true,
		}
		assert.NoError(t, nameFlag.Set("mcp-server.name", "first"))

		body, err := json.Marshal(map[string]any{"foo": outer.Get()})
		assert.NoError(t, err)
		assert.JSONEq(t, `{"foo":[{"name":"first"}]}`, string(body))
	})

	t.Run("nullable object sets field from inner flag", func(t *testing.T) {
		t.Parallel()
		outer := &Flag[any]{Name: "metadata"}
		assert.NoError(t, outer.PreParse())

		keyFlag := &InnerFlag[string]{
			Name: "metadata.key", InnerField: "key", OuterFlag: outer,
		}
		assert.NoError(t, keyFlag.Set("metadata.key", "value"))

		body, err := json.Marshal(map[string]any{"foo": outer.Get()})
		assert.NoError(t, err)
		assert.JSONEq(t, `{"foo":{"key":"value"}}`, string(body))
	})

	t.Run("multiple inner flags merge into the trailing element", func(t *testing.T) {
		t.Parallel()
		outer := &Flag[any]{Name: "mcp-server"}
		assert.NoError(t, outer.PreParse())

		nameFlag := &InnerFlag[string]{
			Name: "mcp-server.name", InnerField: "name",
			OuterFlag: outer, OuterIsArrayOfObjects: true,
		}
		urlFlag := &InnerFlag[string]{
			Name: "mcp-server.url", InnerField: "url",
			OuterFlag: outer, OuterIsArrayOfObjects: true,
		}
		assert.NoError(t, nameFlag.Set("mcp-server.name", "first"))
		assert.NoError(t, urlFlag.Set("mcp-server.url", "https://example.com"))

		body, err := json.Marshal(map[string]any{"foo": outer.Get()})
		assert.NoError(t, err)
		assert.JSONEq(t, `{"foo":[{"name":"first","url":"https://example.com"}]}`, string(body))
	})
}

func TestApplyStdinDataToFlags(t *testing.T) {
	t.Parallel()

	t.Run("sets query path flag from piped data", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:      "account-id",
			QueryPath: "account_id",
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"account_id": "acct_123"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, flag.IsSet())
		assert.Equal(t, "acct_123", flag.Get())
	})

	t.Run("sets header path flag from piped data", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:       "idempotency-key",
			HeaderPath: "Idempotency-Key",
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"Idempotency-Key": "key-xyz"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, flag.IsSet())
		assert.Equal(t, "key-xyz", flag.Get())
	})

	t.Run("does not set body path flag from piped data", func(t *testing.T) {
		t.Parallel()

		// Body params are handled by the maps.Copy merge in flagOptions, not by ApplyStdinDataToFlags.
		flag := &Flag[string]{
			Name:     "message",
			BodyPath: "message",
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"message": "hello world"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.False(t, flag.IsSet())
	})

	t.Run("does not override flag already set via CLI", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:      "account-id",
			QueryPath: "account_id",
		}
		assert.NoError(t, flag.PreParse())
		assert.NoError(t, flag.Set("account-id", "explicit_value"))

		data := map[string]any{"account_id": "piped_value"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		// The explicitly-set value should win.
		assert.Equal(t, "explicit_value", flag.Get())
	})

	t.Run("sets integer query flag from piped data", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[int64]{
			Name:      "page-size",
			QueryPath: "page_size",
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"page_size": int64(50)}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, flag.IsSet())
		assert.Equal(t, int64(50), flag.Get())
	})

	t.Run("sets boolean query flag from piped data", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[bool]{
			Name:      "include-deleted",
			QueryPath: "include_deleted",
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"include_deleted": true}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, flag.IsSet())
		assert.Equal(t, true, flag.Get())
	})

	t.Run("resolves query path flag via data alias", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:        "account-id",
			QueryPath:   "account_id",
			DataAliases: []string{"accountId", "account"},
		}
		assert.NoError(t, flag.PreParse())

		// Use one of the aliases as the key in piped data.
		data := map[string]any{"accountId": "acct_alias"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, flag.IsSet())
		assert.Equal(t, "acct_alias", flag.Get())
	})

	t.Run("does not set body path flag via data alias", func(t *testing.T) {
		t.Parallel()

		// Body params are handled by the maps.Copy merge in flagOptions, not by ApplyStdinDataToFlags.
		flag := &Flag[string]{
			Name:        "user-name",
			BodyPath:    "user_name",
			DataAliases: []string{"userName", "username"},
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"userName": "alice"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.False(t, flag.IsSet())
	})

	t.Run("ignores flags with no matching key in piped data", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:      "account-id",
			QueryPath: "account_id",
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"other_key": "value"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.False(t, flag.IsSet())
	})

	t.Run("ignores flags with no path set", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name: "some-flag",
			// No QueryPath, HeaderPath, or BodyPath
		}
		assert.NoError(t, flag.PreParse())

		data := map[string]any{"some-flag": "value"}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.False(t, flag.IsSet())
	})

	t.Run("handles multiple flags from piped data", func(t *testing.T) {
		t.Parallel()

		accountFlag := &Flag[string]{
			Name:      "account-id",
			QueryPath: "account_id",
		}
		limitFlag := &Flag[int64]{
			Name:      "limit",
			QueryPath: "limit",
		}
		assert.NoError(t, accountFlag.PreParse())
		assert.NoError(t, limitFlag.PreParse())

		data := map[string]any{
			"account_id": "acct_abc",
			"limit":      int64(25),
		}
		cmd := &cli.Command{Flags: []cli.Flag{accountFlag, limitFlag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, accountFlag.IsSet())
		assert.Equal(t, "acct_abc", accountFlag.Get())
		assert.True(t, limitFlag.IsSet())
		assert.Equal(t, int64(25), limitFlag.Get())
	})

	t.Run("sets inner flag from nested piped data under outer body path", func(t *testing.T) {
		t.Parallel()

		outer := &Flag[map[string]any]{
			Name:     "address",
			BodyPath: "address",
		}
		assert.NoError(t, outer.PreParse())

		cityInner := &InnerFlag[string]{
			Name:       "address.city",
			InnerField: "city",
			OuterFlag:  outer,
		}

		data := map[string]any{
			"address": map[string]any{"city": "San Francisco"},
		}
		cmd := &cli.Command{Flags: []cli.Flag{outer, cityInner}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		// InnerFlag.IsSet() is always false by design; verify the value was written
		// into the outer flag's underlying map instead.
		outerVal, ok := outer.Get().(map[string]any)
		assert.True(t, ok, "expected outer flag value to be map[string]any, got %T", outer.Get())
		assert.Equal(t, "San Francisco", outerVal["city"])
	})

	t.Run("sets inner flag via data alias in nested piped data", func(t *testing.T) {
		t.Parallel()

		outer := &Flag[map[string]any]{
			Name:     "address",
			BodyPath: "address",
		}
		assert.NoError(t, outer.PreParse())

		cityInner := &InnerFlag[string]{
			Name:        "address.city",
			InnerField:  "city",
			DataAliases: []string{"cityName"},
			OuterFlag:   outer,
		}

		// Use the alias in piped data.
		data := map[string]any{
			"address": map[string]any{"cityName": "Portland"},
		}
		cmd := &cli.Command{Flags: []cli.Flag{outer, cityInner}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		// InnerFlag.IsSet() is always false by design; verify the value was written
		// into the outer flag's underlying map instead.
		outerVal, ok := outer.Get().(map[string]any)
		assert.True(t, ok, "expected outer flag value to be map[string]any, got %T", outer.Get())
		assert.Equal(t, "Portland", outerVal["city"])
	})

	t.Run("does not set inner flag when outer flag has no body path", func(t *testing.T) {
		t.Parallel()

		outer := &Flag[map[string]any]{
			Name: "options",
			// No BodyPath set
		}
		assert.NoError(t, outer.PreParse())

		inner := &InnerFlag[string]{
			Name:       "options.key",
			InnerField: "key",
			OuterFlag:  outer,
		}

		data := map[string]any{
			"options": map[string]any{"key": "value"},
		}
		cmd := &cli.Command{Flags: []cli.Flag{outer, inner}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.False(t, inner.IsSet())
	})

	t.Run("does not set inner flag when piped data has no nested map for outer path", func(t *testing.T) {
		t.Parallel()

		outer := &Flag[map[string]any]{
			Name:     "address",
			BodyPath: "address",
		}
		assert.NoError(t, outer.PreParse())

		inner := &InnerFlag[string]{
			Name:       "address.city",
			InnerField: "city",
			OuterFlag:  outer,
		}

		// The outer body path key is missing from the piped data.
		data := map[string]any{"other": "value"}
		cmd := &cli.Command{Flags: []cli.Flag{outer, inner}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.False(t, inner.IsSet())
	})

	t.Run("canonical path key takes precedence over alias when both are present", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:        "account-id",
			QueryPath:   "account_id",
			DataAliases: []string{"accountId"},
		}
		assert.NoError(t, flag.PreParse())

		// Both canonical and alias present — canonical should win because it's checked first.
		data := map[string]any{
			"account_id": "canonical_value",
			"accountId":  "alias_value",
		}
		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

		assert.True(t, flag.IsSet())
		assert.Equal(t, "canonical_value", flag.Get())
	})

	t.Run("empty data map does not set any flags", func(t *testing.T) {
		t.Parallel()

		flag := &Flag[string]{
			Name:      "account-id",
			QueryPath: "account_id",
		}
		assert.NoError(t, flag.PreParse())

		cmd := &cli.Command{Flags: []cli.Flag{flag}}
		assert.NoError(t, ApplyStdinDataToFlags(cmd, map[string]any{}))

		assert.False(t, flag.IsSet())
	})
}
