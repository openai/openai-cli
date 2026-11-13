package requestflag

import (
	"fmt"

	"github.com/urfave/cli/v3"
)

// ApplyStdinDataToFlags sets flag values from a parsed stdin data map for flags
// that have not already been set via the command line. This allows piped
// YAML/JSON data to satisfy path, query, and header parameters. Body parameters
// are excluded because they are merged separately. Request locations and inner
// flags are matched by their canonical names or configured data aliases.
func ApplyStdinDataToFlags(cmd *cli.Command, data map[string]any) error {
	return applyStdinDataToFlags(cmd, data, nil)
}

// ApplyStdinDataToFlagsWithProvenance reports every flag populated from stdin.
// Inner flags are reported directly so callers can distinguish an untrusted
// nested field from other, explicitly supplied fields on its outer flag.
func ApplyStdinDataToFlagsWithProvenance(cmd *cli.Command, data map[string]any, onSet func(cli.Flag)) error {
	return applyStdinDataToFlags(cmd, data, onSet)
}

func innerFieldIsSet(inner HasOuterFlag) bool {
	outer := inner.GetOuterFlag()
	if !outer.IsSet() {
		return false
	}
	fields, acceptsPiped := pipedInnerFields(outer)
	if !acceptsPiped {
		return true
	}
	_, exists := fields[inner.GetInnerField()]
	return exists
}

// ProtectPipedInnerValue marks only the field that just came from stdin, so
// explicit siblings retain their own source handling.
func ProtectPipedInnerValue(inner HasOuterFlag, protect func(any) any) {
	fields, _ := pipedInnerFields(inner.GetOuterFlag())
	key := inner.GetInnerField()
	if value, exists := fields[key]; exists {
		fields[key] = protect(value)
	}
}

// A piped inner field targets the last object in an array. Explicit empty
// containers and scalars suppress all piped fields.
func pipedInnerFields(outer cli.Flag) (map[string]any, bool) {
	switch value := unwrappedInput(outer.Get()).(type) {
	case map[string]any:
		return value, len(value) != 0
	case []map[string]any:
		if len(value) != 0 {
			return value[len(value)-1], true
		}
	case []any:
		if len(value) != 0 {
			fields, _ := value[len(value)-1].(map[string]any)
			return fields, true
		}
	}
	return nil, false
}

func setFlagFromStdin(flag cli.Flag, value string, onSet func(cli.Flag)) error {
	if err := flag.Set(flag.Names()[0], value); err != nil {
		return stdinValueError(flag, fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err))
	}
	if onSet != nil {
		onSet(flag)
	}
	return nil
}
