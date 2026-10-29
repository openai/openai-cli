package requestflag

import (
	"fmt"
	"reflect"

	"github.com/urfave/cli/v3"
)

// Query flags accept a whole collection from stdin. Other request locations
// retain their existing formatting and assignment rules.
func setRequestFlagFromStdin(flag cli.Flag, value any, onSet func(cli.Flag)) error {
	if handled, err := setUntypedFlagFromStdin(flag, value, onSet); handled {
		return err
	}
	if setter, ok := flag.(interface{ setStdinCollection(any) (bool, error) }); ok {
		if handled, err := setter.setStdinCollection(value); handled {
			if err != nil {
				return stdinValueError(flag, fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err))
			}
			if onSet != nil {
				onSet(flag)
			}
			return nil
		}
	}
	formatted, err := formatForFlagSet(value)
	if err != nil {
		return stdinValueError(flag, fmt.Errorf("cannot format piped value for flag %q: %w", flag.Names()[0], err))
	}
	return setFlagFromStdin(flag, formatted, onSet)
}

func setUntypedFlagFromStdin(flag cli.Flag, value any, onSet func(cli.Flag)) (bool, error) {
	setter, ok := flag.(interface {
		isUntyped() bool
		setUntypedStdin(any) (bool, error)
	})
	if !ok || !setter.isUntyped() {
		return false, nil
	}
	if _, err := formatForFlagSet(value); err != nil {
		return true, stdinValueError(flag, fmt.Errorf("cannot format piped value for flag %q: %w", flag.Names()[0], err))
	}
	handled, err := setter.setUntypedStdin(value)
	if !handled {
		return false, nil
	}
	if err != nil {
		return true, stdinValueError(flag, fmt.Errorf("cannot set flag %q from piped data: %w", flag.Names()[0], err))
	}
	if onSet != nil {
		onSet(flag)
	}
	return true, nil
}

func (f *Flag[T]) isUntyped() bool { return reflect.TypeFor[T]().Kind() == reflect.Interface }

func (f *Flag[T]) setUntypedStdin(value any) (bool, error) {
	if reflect.TypeFor[T]().Kind() != reflect.Interface {
		return false, nil
	}
	var parsed T
	if value != nil {
		parsed = value.(T)
	}
	if f.Validator != nil {
		if err := f.Validator(parsed); err != nil {
			return true, err
		}
	}
	if !f.applied {
		if err := f.PreParse(); err != nil {
			return true, err
		}
		f.applied = true
	}
	f.value = &cliValue[T]{parsed}
	f.count++
	f.hasBeenSet = true
	return true, nil
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
