package requestflag

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/urfave/cli/v3"
)

type structuredTarget interface {
	inputPolicyFlag
	setStructuredRaw(string) error
	describeStructured(string)
}

// structuredOperand checks the adapter type without converting text to numbers
// or numbers to text. The schema, not a Go interface, selects this entrypoint.
func structuredOperand(raw string, typ reflect.Type, name string) (any, error) {
	var value any
	if err := UnmarshalYAMLOrJSON([]byte(raw), &value); err != nil {
		return nil, structuredInputError(name, "expected JSON or YAML")
	}
	if typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	converted, err := inputAsType(value, typ)
	if err != nil {
		return nil, structuredInputError(name, "incompatible value type")
	}
	return converted.Interface(), nil
}

func inputTyped[T any](value reflect.Value) T {
	var result T
	if value.IsValid() {
		reflect.ValueOf(&result).Elem().Set(value)
	}
	return result
}

func inputAsType(value any, typ reflect.Type) (reflect.Value, error) {
	if value == nil {
		switch typ.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice:
			return reflect.Zero(typ), nil
		}
		return reflect.Value{}, fmt.Errorf("null is not accepted")
	}
	v := reflect.ValueOf(value)
	if v.Type().AssignableTo(typ) {
		return v, nil
	}
	if typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.String && v.Kind() == reflect.String {
		result := reflect.New(typ.Elem())
		result.Elem().SetString(v.String())
		return result, nil
	}
	if typ.Kind() == reflect.Slice && v.Kind() == reflect.Slice {
		result := reflect.MakeSlice(typ, v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			item, err := inputAsType(v.Index(i).Interface(), typ.Elem())
			if err != nil {
				return reflect.Value{}, err
			}
			result.Index(i).Set(item)
		}
		return result, nil
	}
	return reflect.Value{}, fmt.Errorf("incompatible value type")
}

func newStructuredInput(raw, name string, typ reflect.Type) (InputValue, error) {
	parse := func(text string) (any, error) { return structuredOperand(text, typ, name) }
	value := InputValue{Option: "--" + name}
	if strings.HasPrefix(raw, "@") || strings.HasPrefix(raw, "\\@") {
		value.Raw, value.Parse = raw, parse
		return value, nil
	}
	parsed, err := parse(raw)
	if err != nil {
		return value, err
	}
	value.Value = parsed
	return value, nil
}

func knownInput(value InputValue) (any, bool, error) {
	if value.Parse != nil {
		return nil, false, nil
	}
	current := value.Value
	var previous any
	if value.Previous != nil {
		var known bool
		var err error
		previous, known, err = knownInput(*value.Previous)
		if !known || err != nil {
			return nil, known, err
		}
	}
	if value.Combine != nil {
		result, err := value.Combine(previous, current)
		return result, true, err
	}
	return current, true, nil
}

func (f *Flag[T]) setStructuredRaw(raw string) error {
	if f.innerSet {
		return fmt.Errorf("set --%s-structured before nested --%s flags", f.Name, f.Name)
	}
	if f.IsSet() && !f.structured {
		return fmt.Errorf("cannot combine --%s with --%s-structured", f.Name, f.Name)
	}
	input, err := newStructuredInput(raw, f.Name+"-structured", reflect.TypeFor[T]())
	if err != nil {
		return err
	}
	if kind := reflect.TypeFor[T]().Kind(); kind == reflect.Interface || kind == reflect.Slice {
		input.Previous = f.structuredInput
	}
	input.Combine = func(previous, current any) (any, error) {
		result := current
		if previous != nil {
			prior := reflect.ValueOf(previous)
			if prior.Kind() == reflect.Slice {
				values, ok := previous.([]any)
				if !ok {
					values = make([]any, prior.Len(), prior.Len()+1)
					for i := range prior.Len() {
						values[i] = prior.Index(i).Interface()
					}
				}
				result = append(values, current)
			}
		} else if reflect.TypeFor[T]().Kind() == reflect.Slice {
			result = []any{current}
		}
		parsed, err := inputAsType(result, reflect.TypeFor[T]())
		if err != nil {
			return nil, structuredInputError(f.Name+"-structured", "incompatible value type")
		}
		if f.Validator != nil {
			if err := f.Validator(inputTyped[T](parsed)); err != nil {
				return nil, structuredInputError(f.Name+"-structured", "value failed validation")
			}
		}
		return parsed.Interface(), nil
	}
	if known, ok, err := knownInput(input); err != nil {
		return err
	} else if ok {
		input = InputValue{Value: known, Option: input.Option}
		f.value = &cliValue[T]{inputTyped[T](reflect.ValueOf(known))}
	}
	f.structuredInput = &input
	f.structured = true
	f.hasBeenSet = true
	f.count++
	return nil
}

func (f *InnerFlag[T]) setStructuredRaw(raw string) error {
	if f.literal {
		return fmt.Errorf("cannot combine --%s with --%s-structured", f.Name, f.Name)
	}
	input, err := newStructuredInput(raw, f.Name+"-structured", reflect.TypeFor[T]())
	if err != nil {
		return err
	}
	input.Combine = func(_, value any) (any, error) {
		parsed, err := inputAsType(value, reflect.TypeFor[T]())
		if err != nil {
			return nil, structuredInputError(f.Name+"-structured", "incompatible value type")
		}
		if f.Validator != nil {
			if err := f.Validator(inputTyped[T](parsed)); err != nil {
				return nil, structuredInputError(f.Name+"-structured", "value failed validation")
			}
		}
		return parsed.Interface(), nil
	}
	if known, ok, err := knownInput(input); err != nil {
		return err
	} else if ok {
		input = InputValue{Value: known, Option: input.Option}
	}
	if err := f.setInnerValue(input); err != nil {
		return err
	}
	f.structured = true
	return nil
}

func (f *Flag[T]) describeStructured(name string) {
	note := "Text is literal by default. For JSON or YAML, use --" + name + ".\n\n"
	if !strings.HasPrefix(f.Usage, note) {
		f.Usage = note + f.Usage
	}
}
func (f *InnerFlag[T]) describeStructured(name string) {
	note := "Text is literal by default. For JSON or YAML, use --" + name + ".\n\n"
	if !strings.HasPrefix(f.Usage, note) {
		f.Usage = note + f.Usage
	}
}

type structuredFlag struct {
	*Flag[string]
	target structuredTarget
}

func (f *structuredFlag) Set(_ string, raw string) error {
	if err := f.target.setStructuredRaw(raw); err != nil {
		return err
	}
	return f.Flag.Set(f.Name, raw)
}

// AddStructuredFlags adds companions only for schema-declared text unions.
// Shared aliases receive the same companion object and one help annotation.
func AddStructuredFlags(root *cli.Command) {
	companions := map[structuredTarget]*structuredFlag{}
	var visit func(*cli.Command)
	visit = func(command *cli.Command) {
		existing := map[string]bool{}
		for _, flag := range command.Flags {
			for _, name := range flag.Names() {
				existing[name] = true
			}
			if companion, ok := flag.(*structuredFlag); ok {
				companions[companion.target] = companion
			}
		}
		original := append([]cli.Flag(nil), command.Flags...)
		for _, flag := range original {
			target, ok := flag.(structuredTarget)
			if !ok || target.inputPolicy() != InputPolicyTextStructured || !isRequestOperand(flag) {
				continue
			}
			name := flag.Names()[0] + "-structured"
			if existing[name] {
				continue
			}
			companion := companions[target]
			if companion == nil {
				aliases := make([]string, 0, len(flag.Names())-1)
				for _, alias := range flag.Names()[1:] {
					aliases = append(aliases, alias+"-structured")
				}
				companion = &structuredFlag{Flag: &Flag[string]{Name: name, Aliases: aliases, InputPolicy: InputPolicyText, Usage: "Set --" + flag.Names()[0] + " from JSON or YAML, including @file or @-."}, target: target}
				companions[target] = companion
				target.describeStructured(name)
			}
			command.Flags = append(command.Flags, companion)
			existing[name] = true
		}
		for _, child := range command.Commands {
			visit(child)
		}
	}
	visit(root)
}

// StructuredInputError omits rejected values and source contents.
type StructuredInputError struct{ name, reason string }

func (e *StructuredInputError) Error() string {
	return "invalid structured value for --" + e.name + ": " + e.reason
}
func structuredInputError(name, reason string) error {
	return &StructuredInputError{name: name, reason: reason}
}
