package requestflag

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/urfave/cli/v3"
)

// InnerFlag[T] represents a CLI flag for the urfave/cli package that allows setting
// nested fields within other flags. For example, using `--foo.baz` will set the "baz"
// field on a parent flag named `--foo`.
type InnerFlag[
	T []any | []map[string]any | []DateTimeValue | []DateValue | []TimeValue | []string |
		[]float64 | []int64 | []bool | any | map[string]any | DateTimeValue | DateValue | TimeValue |
		string | float64 | int64 | bool |
		*string | *float64 | *int64 | *bool | *DateTimeValue | *DateValue | *TimeValue,
] struct {
	Name        string        // name of the flag
	DefaultText string        // default text of the flag for usage purposes
	Usage       string        // usage string for help output
	Aliases     []string      // aliases that are allowed for this flag
	Validator   func(T) error // custom function to validate this flag value

	OuterFlag   cli.Flag // The flag on which this inner flag will set values
	InnerField  string   // The inner field which this flag will set
	DataAliases []string // alternate names recognized in YAML values passed as the outer flag

	// OuterIsArrayOfObjects tells an untyped outer flag (Flag[any], used for nullable
	// complex schemas) to seed its underlying value as []map[string]any rather than
	// map[string]any before SetInnerField runs. The hint is ignored for typed outer
	// flags whose zero value already carries a dispatchable reflect.Kind.
	OuterIsArrayOfObjects bool
}

// GetDataAliases returns the aliases recognized when parsing inner field keys from piped or flag YAML.
func (f *InnerFlag[T]) GetDataAliases() []string {
	return f.DataAliases
}

// GetInnerField returns the API field name that this inner flag sets on its outer flag's value.
// For example, the flag --parent.foo targeting a parameter whose OpenAPI property name is "foo"
// would return "foo". This is distinct from the flag's CLI name and from any DataAliases entries.
func (f *InnerFlag[T]) GetInnerField() string {
	return f.InnerField
}

type HasOuterFlag interface {
	cli.Flag
	SetOuterFlag(cli.Flag)
	GetOuterFlag() cli.Flag
	GetInnerField() string
	GetDataAliases() []string
}

func (f *InnerFlag[T]) SetOuterFlag(flag cli.Flag) {
	f.OuterFlag = flag
}

func (f *InnerFlag[T]) GetOuterFlag() cli.Flag {
	return f.OuterFlag
}

// Implementation of the cli.Flag interface
var _ cli.Flag = (*InnerFlag[any])(nil) // Type assertion to ensure interface compliance

func (f *InnerFlag[T]) PreParse() error {
	return nil
}

func (f *InnerFlag[T]) PostParse() error {
	return nil
}

func (f *InnerFlag[T]) Set(name string, rawVal string) error {
	if parsedValue, err := parseCLIArg[T](rawVal); err != nil {
		return err
	} else {
		if f.Validator != nil {
			if err := f.Validator(parsedValue); err != nil {
				return err
			}
		}

		if seeder, ok := f.OuterFlag.(InnerFieldSeeder); ok {
			seeder.SeedInnerCollection(f.OuterIsArrayOfObjects)
		}

		if settableInnerField, ok := f.OuterFlag.(SettableInnerField); ok {
			settableInnerField.SetInnerField(f.InnerField, parsedValue)
		} else {
			return fmt.Errorf("Cannot set inner field on %v", f.OuterFlag)
		}
		return nil
	}
}

func (f *InnerFlag[T]) Get() any {
	var zeroValue T
	return zeroValue
}

func (f *InnerFlag[T]) String() string {
	return cli.FlagStringer(f)
}

func (f *InnerFlag[T]) IsSet() bool {
	return false
}

func (f *InnerFlag[T]) Names() []string {
	return cli.FlagNames(f.Name, f.Aliases)
}

// Implementation for the cli.DocGenerationFlag interface
var _ cli.DocGenerationFlag = (*InnerFlag[any])(nil) // Type assertion to ensure interface compliance

func (f *InnerFlag[T]) TakesValue() bool {
	var t T
	return reflect.TypeOf(t) == nil || reflect.TypeOf(t).Kind() != reflect.Bool
}

func (f *InnerFlag[T]) GetUsage() string {
	return f.Usage
}

func (f *InnerFlag[T]) GetValue() string {
	return ""
}

func (f *InnerFlag[T]) GetDefaultText() string {
	return f.DefaultText
}

func (f *InnerFlag[T]) GetEnvVars() []string {
	return nil
}

func (f *InnerFlag[T]) IsDefaultVisible() bool {
	return false
}

func (f *InnerFlag[T]) TypeName() string {
	var zeroValue T
	ty := reflect.TypeOf(zeroValue)
	if ty == nil {
		return ""
	}
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
var _ cli.DocGenerationMultiValueFlag = (*InnerFlag[any])(nil) // Type assertion to ensure interface compliance

func (f *InnerFlag[T]) IsMultiValueFlag() bool {
	return false
}

func (f *InnerFlag[T]) IsBoolFlag() bool {
	var zeroValue T
	_, isBool := any(zeroValue).(bool)
	return isBool
}

// WithInnerFlags takes a command and a map of flag names to inner flags,
// and returns a modified command with the appropriate inner flags set.
func WithInnerFlags(cmd cli.Command, innerFlagMap map[string][]HasOuterFlag) cli.Command {
	if len(innerFlagMap) == 0 {
		return cmd
	}

	// If any keys are unused by the end, we know that they were not valid
	unusedInnerFlagKeys := make(map[string]struct{})
	for name := range innerFlagMap {
		unusedInnerFlagKeys[name] = struct{}{}
	}

	updatedFlags := make([]cli.Flag, 0, len(cmd.Flags))
	for _, flag := range cmd.Flags {
		updatedFlags = append(updatedFlags, flag)
		for _, name := range flag.Names() {
			// Check if this flag has inner flags in our map
			innerFlags, hasInnerFlags := innerFlagMap[name]
			if !hasInnerFlags {
				continue
			}

			// Mark this inner flag key as used
			delete(unusedInnerFlagKeys, name)

			for _, innerFlag := range innerFlags {
				innerFlag.SetOuterFlag(flag)
				updatedFlags = append(updatedFlags, innerFlag)
			}
		}
	}

	// If there are inner flags that don't correspond to any valid outer flag
	// names, then panic because the user probably made a typo or forgot to
	// delete inner flags that correspond to missing outer flags.
	if len(unusedInnerFlagKeys) > 0 {
		unusedKeys := make([]string, 0, len(unusedInnerFlagKeys))
		for key := range unusedInnerFlagKeys {
			unusedKeys = append(unusedKeys, key)
		}
		panic(fmt.Sprintf("Missing outer flags to use with inner flags: %v", unusedKeys))
	}

	result := cmd
	result.Flags = updatedFlags
	return result
}

// Helper function to verify that all inner flags have an outer flag set and
// follow the --foo.baz prefix format
func CheckInnerFlags(cmd cli.Command) error {
	var errors []string
	for _, flag := range cmd.Flags {
		if innerFlag, ok := flag.(HasOuterFlag); ok {
			outerFlag := innerFlag.GetOuterFlag()
			if outerFlag == nil {
				errors = append(errors, fmt.Sprintf("inner flag %s is missing an outer flag", flag.Names()))
				continue
			}

			innerFlagName := flag.Names()[0]
			valid := false
			for _, outerName := range outerFlag.Names() {
				if strings.HasPrefix(innerFlagName, outerName+".") {
					valid = true
					break
				}
			}

			if !valid {
				errors = append(errors, fmt.Sprintf("inner flag %s must start with one of its outer flag's names followed by a dot", innerFlagName))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return nil
}
