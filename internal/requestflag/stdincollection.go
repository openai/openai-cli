package requestflag

import (
	"fmt"
	"reflect"

	"github.com/urfave/cli/v3"
)

// Query flags accept a whole collection from stdin. Other request locations
// retain their existing formatting and assignment rules.
func setRequestFlagFromStdin(flag cli.Flag, value any, onSet func(cli.Flag)) error {
	if setter, ok := flag.(interface{ setStdinCollection(any) (bool, error) }); ok {
		if handled, err := setter.setStdinCollection(value); handled {
			if err != nil {
				return fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err)
			}
			if onSet != nil {
				onSet(flag)
			}
			return nil
		}
	}
	formatted, err := formatForFlagSet(value)
	if err != nil {
		return fmt.Errorf("cannot format piped value for flag %q: %w", flag.Names()[0], err)
	}
	return setFlagFromStdin(flag, formatted, onSet)
}

func (f *Flag[T]) setStdinCollection(value any) (bool, error) {
	input := reflect.ValueOf(value)
	typ := reflect.TypeFor[T]()
	if f.QueryPath == "" || input.Kind() != reflect.Slice || typ.Kind() != reflect.Slice {
		return false, nil
	}

	// Convert into a fresh value so errors cannot leave a partially assigned flag.
	// Reuse element conversion to match repeated CLI arguments, without changing
	// Set's append semantics or appending to a default collection.
	var collection T
	if !input.IsNil() {
		collection = reflect.MakeSlice(typ, 0, input.Len()).Interface().(T)
	}
	parsed := &cliValue[T]{collection}
	for i := 0; i < input.Len(); i++ {
		element, err := formatForFlagSet(input.Index(i).Interface())
		if err != nil {
			return true, err
		}
		if err := parsed.Set(element); err != nil {
			return true, err
		}
	}
	if !f.applied {
		if err := f.PreParse(); err != nil {
			return true, err
		}
	}
	if f.Validator != nil {
		if err := f.Validator(parsed.value); err != nil {
			return true, err
		}
	}
	f.value = parsed
	f.count++
	f.hasBeenSet = true
	return true, nil
}
