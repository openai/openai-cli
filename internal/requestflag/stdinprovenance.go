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

func innerMapFieldIsSet(inner HasOuterFlag) bool {
	outer := inner.GetOuterFlag()
	if !outer.IsSet() {
		return false
	}
	values, ok := outer.Get().(map[string]any)
	if !ok {
		return false
	}
	_, exists := values[inner.GetInnerField()]
	return exists
}

func setFlagFromStdin(flag cli.Flag, value string, onSet func(cli.Flag)) error {
	if err := flag.Set(flag.Names()[0], value); err != nil {
		return fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err)
	}
	if onSet != nil {
		onSet(flag)
	}
	return nil
}
