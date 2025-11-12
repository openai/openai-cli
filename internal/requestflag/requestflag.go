package requestflag

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/goccy/go-yaml"
	"github.com/urfave/cli/v3"
)

// formatForFlagSet converts a Go value parsed from YAML/JSON stdin data into a string
// that flag.Set (and thus parseCLIArg) can parse correctly for each flag type.
// Strings are returned as-is (parseCLIArg[string] assigns the raw value directly, so
// JSON-quoting must be avoided). Scalars use %v. Complex types (maps, slices) are
// JSON-encoded, which the yaml.Unmarshal default branch in parseCLIArg can parse.
func formatForFlagSet(val any) (string, error) {
	switch v := val.(type) {
	case string:
		return v, nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", val), nil
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("cannot format value %T for flag.Set: %w", val, err)
		}
		return string(b), nil
	}
}

// Flag [T] is a generic flag base which can be used to implement the most
// common interfaces used by urfave/cli. Additionally, it allows specifying
// where in an HTTP request the flag values should be placed (e.g. query, body, etc.).
//
// Pointer-to-primitive type parameters (e.g. *string) are used for flags whose underlying
// schema is nullable. They give flags a tri-state: unset (excluded from the request),
// set to the literal "null" (nil pointer → JSON null), or set to a value (*v → JSON value).
type Flag[
	T []any | []map[string]any | []DateTimeValue | []DateValue | []TimeValue | []string |
		[]float64 | []int64 | []bool | any | map[string]any | DateTimeValue | DateValue | TimeValue |
		string | float64 | int64 | bool |
		*string | *float64 | *int64 | *bool | *DateTimeValue | *DateValue | *TimeValue,
] struct {
	Name        string               // name of the flag
	Category    string               // category of the flag, if any
	DefaultText string               // default text of the flag for usage purposes
	HideDefault bool                 // whether to hide the default value in output
	Usage       string               // usage string for help output
	Sources     cli.ValueSourceChain // sources to load flag value from
	Required    bool                 // whether the flag is required or not
	Hidden      bool                 // whether to hide the flag in help output
	Default     T                    // default value for this flag if not set by from any source
	Aliases     []string             // aliases that are allowed for this flag
	Validator   func(T) error        // custom function to validate this flag value

	QueryPath  string // location in the request query string to put this flag's value
	HeaderPath string // location in the request header to put this flag's value
	BodyPath   string // location in the request body to put this flag's value
	BodyRoot   bool   // if true, then use this value as the entire request body
	PathParam  string // name of the URL path parameter this flag's value maps to

	// Const, when true, marks this flag as a constant. The flag's Default value is used as the fixed value
	// and always included in the request (IsSet returns true). The user can still see and override the flag,
	// but isn't required to provide it. This is used for single-value enums and `x-stainless-const`
	// parameters.
	Const bool

	// FileInput, when true, indicates that the flag value is always treated as a file path. The file is read
	// automatically without requiring the "@" prefix. This is used for parameters with `type: string, format:
	// binary` in the OpenAPI spec.
	FileInput bool

	// DataAliases is a list of alternate names for this parameter recognized when parsing piped YAML/JSON
	// input. Values keyed by any alias are translated to the canonical API name before being sent.
	DataAliases []string

	// unexported fields for internal use
	count      int       // number of times the flag has been set
	hasBeenSet bool      // whether the flag has been set from env or file
	applied    bool      // whether the flag has been applied to a flag set already
	value      cli.Value // value representing this flag's value
}

// Type assertions to verify we implement the relevant urfave/cli interfaces
var _ cli.CategorizableFlag = (*Flag[any])(nil)

// InRequest interface for flags that should be included in HTTP requests
type InRequest interface {
	GetQueryPath() string
	GetHeaderPath() string
	GetBodyPath() string
	GetPathParam() string
	IsBodyRoot() bool
	IsFileInput() bool
	GetDataAliases() []string
}

func (f Flag[T]) GetQueryPath() string {
	return f.QueryPath
}

func (f Flag[T]) GetHeaderPath() string {
	return f.HeaderPath
}

func (f Flag[T]) GetBodyPath() string {
	return f.BodyPath
}

func (f Flag[T]) GetPathParam() string {
	return f.PathParam
}

func (f Flag[T]) IsBodyRoot() bool {
	return f.BodyRoot
}

func (f Flag[T]) IsFileInput() bool {
	return f.FileInput
}

func (f Flag[T]) GetDataAliases() []string {
	return f.DataAliases
}

// The values that will be sent in different parts of a request.
type RequestContents struct {
	Queries map[string]any
	Headers map[string]any
	Body    any
}

// ApplyStdinDataToFlags sets flag values from a parsed stdin data map for flags that have not already been
// set via the command line. This allows piped YAML/JSON data to satisfy path, query, and header parameters.
// Body parameters are excluded: they are already handled by the maps.Copy merge in flagOptions.
// For each unset flag, if the parsed data map contains a key matching the flag's QueryPath, HeaderPath, or
// PathParam (or any of its DataAliases), the flag is set to that value via flag.Set.
//
// Inner flags (those with an outer flag) are also handled: if the outer flag's body path key exists in the
// data map and contains a nested map with a key matching the inner flag's field (or aliases), the inner
// flag is set from that nested value.
func ApplyStdinDataToFlags(cmd *cli.Command, data map[string]any) error {
	for _, flag := range cmd.Flags {
		if flag.IsSet() {
			continue
		}

		// Handle inner flags: look for their value nested under the outer flag's body path.
		if inner, ok := flag.(HasOuterFlag); ok {
			outer, outerOk := inner.GetOuterFlag().(InRequest)
			if !outerOk || outer.GetBodyPath() == "" {
				continue
			}
			nested, ok := data[outer.GetBodyPath()].(map[string]any)
			if !ok {
				continue
			}
			innerField := inner.GetInnerField()
			val, found := nested[innerField]
			if !found {
				for _, alias := range inner.GetDataAliases() {
					if alias != "" && alias != innerField {
						if v, ok := nested[alias]; ok {
							val, found = v, true
							break
						}
					}
				}
			}
			if !found {
				continue
			}
			setVal, err := formatForFlagSet(val)
			if err != nil {
				return fmt.Errorf("cannot format piped value for flag %q: %w", flag.Names()[0], err)
			}
			if err := flag.Set(flag.Names()[0], setVal); err != nil {
				return fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err)
			}
			continue
		}

		inReq, ok := flag.(InRequest)
		if !ok {
			continue
		}

		// Try each request location in turn, checking the canonical path key and all aliases.
		// Body params are excluded: they are already handled by the maps.Copy merge in flagOptions.
		for _, path := range []string{inReq.GetQueryPath(), inReq.GetHeaderPath(), inReq.GetPathParam()} {
			if path == "" {
				continue
			}
			var val any
			var found bool
			for _, key := range append([]string{path}, inReq.GetDataAliases()...) {
				if v, ok := data[key]; ok {
					val, found = v, true
					break
				}
			}
			if !found {
				continue
			}
			setVal, err := formatForFlagSet(val)
			if err != nil {
				return fmt.Errorf("cannot format piped value for flag %q: %w", flag.Names()[0], err)
			}
			if err := flag.Set(flag.Names()[0], setVal); err != nil {
				return fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err)
			}
			break
		}
	}
	return nil
}

func ExtractRequestContents(cmd *cli.Command) RequestContents {
	bodyMap := make(map[string]any)
	res := RequestContents{
		Queries: make(map[string]any),
		Headers: make(map[string]any),
		Body:    bodyMap,
	}

	for _, flag := range cmd.Flags {
		if !flag.IsSet() {
			continue
		}

		value := flag.Get()
		if toSend, ok := flag.(InRequest); ok {
			if queryPath := toSend.GetQueryPath(); queryPath != "" {
				res.Queries[queryPath] = value
			}
			if headerPath := toSend.GetHeaderPath(); headerPath != "" {
				res.Headers[headerPath] = value
			}
			if toSend.IsBodyRoot() {
				res.Body = value
			} else if bodyPath := toSend.GetBodyPath(); bodyPath != "" {
				bodyMap[bodyPath] = value
			}
		}
	}
	return res
}

func GetMissingRequiredFlags(cmd *cli.Command, body any) []cli.Flag {
	missing := []cli.Flag{}
	for _, flag := range cmd.Flags {
		if flag.IsSet() {
			continue
		}

		if required, ok := flag.(cli.RequiredFlag); ok && required.IsRequired() {
			missing = append(missing, flag)
			continue
		}

		if r, ok := flag.(RequiredFlagOrStdin); !ok || !r.IsRequiredAsFlagOrStdin() {
			continue
		}

		if toSend, ok := flag.(InRequest); ok {
			if toSend.IsBodyRoot() {
				if body != nil {
					continue
				}
			} else if bodyPath := toSend.GetBodyPath(); bodyPath != "" {
				if bodyMap, ok := body.(map[string]any); ok {
					if _, found := bodyMap[bodyPath]; found {
						continue
					}
				}
			}
		}
		missing = append(missing, flag)
	}
	return missing
}

// Implementation of the cli.Flag interface
var _ cli.Flag = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f *Flag[T]) PreParse() error {
	newVal := f.Default
	f.value = &cliValue[T]{newVal}

	// Validate the given default or values set from external sources as well
	if f.Validator != nil {
		if err := f.Validator(f.value.Get().(T)); err != nil {
			return err
		}
	}
	f.applied = true
	return nil
}

func (f *Flag[T]) PostParse() error {
	if !f.hasBeenSet {
		if val, source, found := f.Sources.LookupWithSource(); found {
			if val != "" || reflect.TypeOf(f.value).Kind() == reflect.String {
				if err := f.Set(f.Name, val); err != nil {
					return fmt.Errorf(
						"could not parse %[1]q as %[2]T value from %[3]s for flag %[4]s: %[5]s",
						val, f.value, source, f.Name, err,
					)
				}
			} else if val == "" && reflect.TypeOf(f.value).Kind() == reflect.Bool {
				_ = f.Set(f.Name, "false")
			}

			f.hasBeenSet = true
		}
	}
	return nil
}

func (f *Flag[T]) Set(name string, val string) error {
	// Initialize flag if needed
	if !f.applied {
		if err := f.PreParse(); err != nil {
			return err
		}
		f.applied = true
	}

	f.count++

	// If this is the first time setting a slice type, reset it to empty
	// to avoid appending to the default value
	if f.count == 1 && f.value != nil {
		typ := reflect.TypeOf(f.Default)
		if typ != nil && typ.Kind() == reflect.Slice {
			// Create a new empty slice of the same type and set it
			emptySlice := reflect.MakeSlice(typ, 0, 0).Interface()
			f.value = &cliValue[T]{emptySlice.(T)}
		}
	}

	if err := f.value.Set(val); err != nil {
		return err
	}

	f.hasBeenSet = true

	if f.Validator != nil {
		if err := f.Validator(f.value.Get().(T)); err != nil {
			return err
		}
	}
	return nil
}

func (f *Flag[T]) Get() any {
	if f.value != nil {
		return f.value.Get()
	}
	return f.Default
}

func (f *Flag[T]) String() string {
	return cli.FlagStringer(f)
}

func (f *Flag[T]) IsSet() bool {
	return f.hasBeenSet || f.Const
}

func (f *Flag[T]) Names() []string {
	return cli.FlagNames(f.Name, f.Aliases)
}

// Implementation for the cli.VisibleFlag interface
var _ cli.VisibleFlag = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f *Flag[T]) IsVisible() bool {
	return !f.Hidden
}

func (f *Flag[T]) GetCategory() string {
	return f.Category
}

func (f *Flag[T]) SetCategory(c string) {
	f.Category = c
}

// Implementation for the cli.RequiredFlag interface
var _ cli.RequiredFlag = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f *Flag[T]) IsRequired() bool {
	// Const flags are always auto-set, so never required from the user.
	if f.Const {
		return false
	}
	// Intentionally don't use `f.Required`, because request flags may be passed
	// over stdin as well as by flag.
	if f.BodyPath != "" || f.BodyRoot || f.PathParam != "" || f.QueryPath != "" || f.HeaderPath != "" {
		return false
	}
	return f.Required
}

type RequiredFlagOrStdin interface {
	IsRequiredAsFlagOrStdin() bool
}

func (f *Flag[T]) IsRequiredAsFlagOrStdin() bool {
	// Const flags are always auto-set, so never required from the user.
	if f.Const {
		return false
	}
	return f.Required
}

// Implementation for the cli.DocGenerationFlag interface
var _ cli.DocGenerationFlag = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f *Flag[T]) TakesValue() bool {
	var t T
	return reflect.TypeOf(t) == nil || reflect.TypeOf(t).Kind() != reflect.Bool
}

func (f *Flag[T]) GetUsage() string {
	return f.Usage
}

func (f *Flag[T]) GetValue() string {
	if f.value == nil {
		return ""
	}
	return f.value.String()
}

func (f *Flag[T]) GetDefaultText() string {
	return f.DefaultText
}

// GetEnvVars returns the env vars for this flag
func (f *Flag[T]) GetEnvVars() []string {
	return f.Sources.EnvKeys()
}

func (f *Flag[T]) IsDefaultVisible() bool {
	return !f.HideDefault
}

func (f *Flag[T]) TypeName() string {
	ty := reflect.TypeOf(f.Default)
	if ty == nil {
		return ""
	}
	// Deref pointer-typed flags so --help surfaces the pointee kind (e.g. "string"), not
	// Go's pointer syntax.
	if ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}

	// Get base type name with special handling for built-in types
	getTypeName := func(t reflect.Type) string {
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return "int"
		case reflect.Float32, reflect.Float64:
			return "float"
		case reflect.Bool:
			return "boolean"
		case reflect.String:
			switch t.Name() {
			case "DateTimeValue":
				return "datetime"
			case "DateValue":
				return "date"
			case "TimeValue":
				return "time"
			default:
				return "string"
			}
		default:
			if t.Name() == "" {
				return "any"
			}
			return strings.ToLower(t.Name())
		}
	}

	switch ty.Kind() {
	case reflect.Slice:
		elemType := ty.Elem()
		return getTypeName(elemType)
	case reflect.Map:
		keyType := ty.Key()
		valueType := ty.Elem()
		return fmt.Sprintf("%s=%s", getTypeName(keyType), getTypeName(valueType))
	default:
		return getTypeName(ty)
	}
}

// Implementation for the cli.DocGenerationMultiValueFlag interface
var _ cli.DocGenerationMultiValueFlag = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f *Flag[T]) IsMultiValueFlag() bool {
	if reflect.TypeOf(f.Default) == nil {
		return false
	}
	kind := reflect.TypeOf(f.Default).Kind()
	return kind == reflect.Slice || kind == reflect.Map
}

func (f *Flag[T]) IsBoolFlag() bool {
	// Flag[*bool] is deliberately not treated as a bool flag — the pointer form needs an
	// explicit value (`--foo true`, `--foo null`) to disambiguate the tri-state.
	_, isBool := any(f.Default).(bool)
	return isBool
}

// Implementation for the cli.Countable interface
var _ cli.Countable = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f *Flag[T]) Count() int {
	return f.count
}

// Implementation for the cli.LocalFlag interface
var _ cli.LocalFlag = (*Flag[any])(nil) // Type assertion to ensure interface compliance

func (f Flag[T]) IsLocal() bool {
	// By default, all request flags are local, i.e. can be provided at any part of the CLI command.
	return true
}

// cliValue is a generic implementation of cli.Value for common types
type cliValue[
	T []any | []map[string]any | []DateTimeValue | []DateValue | []TimeValue | []string | []float64 |
		[]int64 | []bool | any | map[string]any | DateTimeValue | DateValue | TimeValue | string |
		float64 | int64 | bool |
		*string | *float64 | *int64 | *bool | *DateTimeValue | *DateValue | *TimeValue,
] struct {
	value T
}

// Take an argument string for a single argument and convert it into a typed
// value for one of the supported CLI argument types
func parseCLIArg[
	T []any | []map[string]any | []DateTimeValue | []DateValue | []TimeValue | []string | []float64 |
		[]int64 | []bool | any | map[string]any | DateTimeValue | DateValue | TimeValue | string |
		float64 | int64 | bool |
		*string | *float64 | *int64 | *bool | *DateTimeValue | *DateValue | *TimeValue,
](value string) (T, error) {
	var parsedValue any
	var err error

	var empty T

	if value == "null" {
		switch any(empty).(type) {
		// Pointer-to-primitive: explicit nil gives the tri-state its "null" state
		// (unset / null / value). Without this, numeric flags would fail to parse
		// "null" and string flags would accept the literal word as a raw value.
		case *string, *int64, *float64, *bool, *DateValue, *DateTimeValue, *TimeValue:
			return empty, nil
		// Maps marshal nil as JSON null natively; short-circuit avoids a YAML round-trip.
		case map[string]any:
			return empty, nil
		}
	}

	switch any(empty).(type) {
	case string:
		parsedValue = value
	case int64:
		parsedValue, err = strconv.ParseInt(value, 0, 64)
	case float64:
		parsedValue, err = strconv.ParseFloat(value, 64)
	case bool:
		parsedValue, err = strconv.ParseBool(value)
	case DateTimeValue:
		var dt DateTimeValue
		err = (&dt).Parse(value)
		if err == nil {
			parsedValue = dt
		}

	case DateValue:
		var d DateValue
		err = (&d).Parse(value)
		if err == nil {
			parsedValue = d
		}

	case TimeValue:
		var t TimeValue
		err = (&t).Parse(value)
		if err == nil {
			parsedValue = t
		}

	// Pointer-to-primitive flags reach here only when `value != "null"`; we parse the
	// pointee type and return its address so JSON marshaling emits the underlying value.
	case *string:
		v := value
		parsedValue = &v
	case *int64:
		var v int64
		v, err = strconv.ParseInt(value, 0, 64)
		if err == nil {
			parsedValue = &v
		}
	case *float64:
		var v float64
		v, err = strconv.ParseFloat(value, 64)
		if err == nil {
			parsedValue = &v
		}
	case *bool:
		var v bool
		v, err = strconv.ParseBool(value)
		if err == nil {
			parsedValue = &v
		}
	case *DateTimeValue:
		var dt DateTimeValue
		err = (&dt).Parse(value)
		if err == nil {
			parsedValue = &dt
		}
	case *DateValue:
		var d DateValue
		err = (&d).Parse(value)
		if err == nil {
			parsedValue = &d
		}
	case *TimeValue:
		var t TimeValue
		err = (&t).Parse(value)
		if err == nil {
			parsedValue = &t
		}

	default:
		if strings.HasPrefix(value, "@") {
			// File literals like @file.txt should work here
			parsedValue = value
		} else {
			var yamlValue T
			err = yaml.Unmarshal([]byte(value), &yamlValue)
			if err == nil {
				parsedValue = yamlValue
			} else if allowAsLiteralString(value) {
				parsedValue = value
			} else {
				parsedValue = nil
				err = fmt.Errorf("failed to parse as YAML: %w", err)
			}
		}
	}

	// Nil needs to be handled specially because unmarshalling a YAML `null`
	// causes problems when doing type assertions.
	if parsedValue == nil {
		parsedValue = (*struct{})(nil)
	}

	if err == nil {
		if typedValue, ok := parsedValue.(T); ok {
			return typedValue, nil
		} else {
			expectedType := reflect.TypeFor[T]()
			err = fmt.Errorf("Couldn't convert %q (%v) to expected type %v", value, parsedValue, expectedType)
		}
	}
	return empty, err

}

// Ptr returns a pointer to its argument. It is used to initialize `Default` on pointer-typed
// Flag values, since Go does not allow taking the address of a composite literal's element
// or of an untyped constant.
func Ptr[T any](v T) *T {
	return &v
}

// Assuming this string failed to parse as valid YAML, this function will
// return true for strings that can reasonably be interpreted as a string literal,
// like identifiers (`foo_bar`), UUIDs (`945b2f0c-8e89-487a-b02c-f851c69ea459`),
// base64 (`aGVsbG8=`), and qualified identifiers (`color.Red`). This should
// not include strings that look like mistyped YAML (e.g. `{key:`)
func allowAsLiteralString(s string) bool {
	for _, c := range s {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) &&
			c != '_' && c != '-' && c != '.' && c != '=' {
			return false
		}
	}
	return true
}

// Parse the input string and set result as the cliValue's value
func (c *cliValue[T]) Set(value string) error {
	valueType := reflect.TypeOf(c.value)
	// When setting slice values, we append to the existing values
	// e.g. --foo 10 --foo 20 --foo 30 => [10, 20, 30]
	if valueType != nil && valueType.Kind() == reflect.Slice {
		elemType := valueType.Elem()

		var singleElem any
		var err error
		switch elemType.Kind() {
		case reflect.String:
			singleElem, err = parseCLIArg[string](value)
		case reflect.Int64:
			singleElem, err = parseCLIArg[int64](value)
		case reflect.Float64:
			singleElem, err = parseCLIArg[float64](value)
		case reflect.Bool:
			singleElem, err = parseCLIArg[bool](value)
		default:
			// Check for special types by name
			switch elemType.Name() {
			case "DateTimeValue":
				singleElem, err = parseCLIArg[DateTimeValue](value)
			case "DateValue":
				singleElem, err = parseCLIArg[DateValue](value)
			case "TimeValue":
				singleElem, err = parseCLIArg[TimeValue](value)
			default:
				// This handles []map[string]any
				if elemType.Kind() == reflect.Map && elemType.Key().Kind() == reflect.String {
					singleElem, err = parseCLIArg[map[string]any](value)
				} else {
					singleElem, err = parseCLIArg[any](value)
				}
			}
		}

		if err != nil {
			return err
		}

		// Append the new element to the slice
		sliceValue := reflect.ValueOf(c.value)
		if !sliceValue.IsValid() || sliceValue.IsNil() {
			// Create a new slice if the current one is nil
			sliceValue = reflect.MakeSlice(valueType, 0, 1)
		}

		// Append the new element
		newElem := reflect.ValueOf(singleElem)
		sliceValue = reflect.Append(sliceValue, newElem)

		// Set the updated slice back to c.value
		c.value = sliceValue.Interface().(T)
	} else {
		// For non-slice types, simply parse and set the value
		if parsedValue, err := parseCLIArg[T](value); err != nil {
			return err
		} else {
			c.value = parsedValue
		}
	}

	return nil
}

func (c *cliValue[T]) Get() any {
	return c.value
}

func (c *cliValue[T]) String() string {
	switch v := any(c.value).(type) {
	case string, int, int64, float64, bool, DateTimeValue, DateValue, TimeValue,
		[]string, []int, []int64, []float64, []bool, []DateTimeValue, []DateValue, []TimeValue:
		// For basic types, use standard string representation
		return fmt.Sprintf("%v", v)

	case *string, *int64, *float64, *bool, *DateTimeValue, *DateValue, *TimeValue:
		// Pointer-to-primitive: nil renders as "null" (the CLI literal that produces it);
		// non-nil derefs to the pointee's standard representation.
		rv := reflect.ValueOf(v)
		if rv.IsNil() {
			return "null"
		}
		return fmt.Sprintf("%v", rv.Elem().Interface())

	default:
		// For complex types, convert to YAML
		yamlBytes, err := yaml.MarshalWithOptions(c.value, yaml.Flow(true))
		if err != nil {
			// Fall back to standard format if YAML conversion fails
			return fmt.Sprintf("%v", c.value)
		}
		return string(yamlBytes)
	}
}

func (c *cliValue[T]) IsBoolFlag() bool {
	_, ok := any(c.value).(bool)
	return ok
}

// Time-related value types
type DateValue string
type DateTimeValue string
type TimeValue string

// String methods for time-related types
func (d DateValue) String() string {
	return string(d)
}

func (d DateTimeValue) String() string {
	return string(d)
}

func (t TimeValue) String() string {
	return string(t)
}

// parseTimeWithFormats attempts to parse a string using multiple formats
func parseTimeWithFormats(s string, formats []string) (time.Time, error) {
	var lastErr error
	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

// Parse methods for time-related types
func (d *DateValue) Parse(s string) error {
	formats := []string{
		"2006-01-02",
		"01/02/2006",
		"Jan 2, 2006",
		"January 2, 2006",
		"2-Jan-2006",
	}

	t, err := parseTimeWithFormats(s, formats)
	if err != nil {
		return fmt.Errorf("unable to parse date: %v", err)
	}

	*d = DateValue(t.Format("2006-01-02"))
	return nil
}

func (d *DateTimeValue) Parse(s string) error {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		time.RFC1123,
		time.RFC822,
		time.ANSIC,
	}

	t, err := parseTimeWithFormats(s, formats)
	if err != nil {
		return fmt.Errorf("unable to parse datetime: %v", err)
	}

	*d = DateTimeValue(t.Format(time.RFC3339))
	return nil
}

func (t *TimeValue) Parse(s string) error {
	formats := []string{
		"15:04:05",
		"15:04:05.999999999Z07:00",
		"3:04:05PM",
		"3:04 PM",
		"15:04",
		time.Kitchen,
	}

	parsedTime, err := parseTimeWithFormats(s, formats)
	if err != nil {
		return fmt.Errorf("unable to parse time: %v", err)
	}

	*t = TimeValue(parsedTime.Format("15:04:05"))
	return nil
}

// Allow setting inner fields on other flags (e.g. --foo.baz can set the "baz"
// field on the --foo flag)
type SettableInnerField interface {
	SetInnerField(string, any)
}

// InnerFieldSeeder lets an InnerFlag prepare its outer flag's underlying value
// before dispatching SetInnerField. This is only meaningful for Flag[any] —
// the codegen output for nullable complex schemas — whose untyped-nil zero
// value would otherwise have no reflect.Kind for the inner-field switch to
// dispatch on.
type InnerFieldSeeder interface {
	SeedInnerCollection(isArrayOfObjects bool)
}

func (f *Flag[T]) SetInnerField(field string, val any) {
	if f.value == nil {
		f.value = &cliValue[T]{}
	}

	if settableInnerField, ok := f.value.(SettableInnerField); ok {
		settableInnerField.SetInnerField(field, val)
		f.hasBeenSet = true
	} else {
		panic(fmt.Sprintf("Cannot set inner field: %v", f.value))
	}
}

// SeedInnerCollection initializes a Flag[any]'s underlying value as an empty
// map[string]any or []map[string]any so subsequent SetInnerField calls have a
// dispatchable reflect.Kind. For typed Flag[T] this is a no-op: the type
// assertion fails and the existing reflect.Kind on the typed-nil zero value
// already routes correctly.
func (f *Flag[T]) SeedInnerCollection(isArrayOfObjects bool) {
	if f.value == nil {
		f.value = &cliValue[T]{}
	}
	cv, ok := f.value.(*cliValue[T])
	if !ok {
		return
	}
	if reflect.ValueOf(cv.value).Kind() != reflect.Invalid {
		return
	}
	if isArrayOfObjects {
		if seed, ok := any([]map[string]any{}).(T); ok {
			cv.value = seed
		}
		return
	}
	if seed, ok := any(map[string]any{}).(T); ok {
		cv.value = seed
	}
}

func (c *cliValue[T]) SetInnerField(field string, val any) {
	flagVal := c.value
	flagValReflect := reflect.ValueOf(flagVal)
	switch flagValReflect.Kind() {
	case reflect.Slice:
		if flagValReflect.Type().Elem().Kind() != reflect.Map {
			return
		}

		sliceLen := flagValReflect.Len()
		if sliceLen > 0 {
			// Check if the last element already has the InnerField
			lastElement := flagValReflect.Index(sliceLen - 1).Interface().(map[string]any)
			if _, hasInnerField := lastElement[field]; !hasInnerField {
				// Last element doesn't have the field, set it
				lastElement[field] = val
				return
			}
		}

		// Create a new map and append it to the slice
		newMap := map[string]any{field: val}
		switch sliceVal := any(c.value).(type) {
		case []map[string]any:
			c.value = any(append(sliceVal, newMap)).(T)
		case []any:
			c.value = any(append(sliceVal, newMap)).(T)
		}

	case reflect.Map:
		mapVal, ok := any(flagVal).(map[string]any)
		if !ok || mapVal == nil {
			mapVal = map[string]any{field: val}
			c.value = any(mapVal).(T)
		} else {
			mapVal[field] = val
		}
	}
}
