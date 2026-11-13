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

// decodeStructuredOperand checks the adapter type without converting text to numbers
// or numbers to text. The schema, not a Go interface, selects this entrypoint.
func decodeStructuredOperand(raw string, typ reflect.Type, name string) (any, error) {
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

func (f *Flag[T]) setStructuredRaw(raw string) error {
	if f.innerSet {
		return fmt.Errorf("set --%s-structured before nested --%s flags", f.Name, f.Name)
	}
	if f.IsSet() && !f.structured {
		return fmt.Errorf("cannot combine --%s with --%s-structured", f.Name, f.Name)
	}
	kind := reflect.TypeFor[T]().Kind()
	var previous *InputValue
	if kind == reflect.Interface || kind == reflect.Slice {
		previous = f.structuredInput
	}
	input, err := structuredFlagInput(raw, f.Name+"-structured", f.Validator, true, previous)
	if err != nil {
		return err
	}
	if len(input.pending) == 0 {
		f.value = &cliValue[T]{inputTyped[T](reflect.ValueOf(input.value))}
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
	input, err := structuredFlagInput(raw, f.Name+"-structured", f.Validator, false, nil)
	if err != nil {
		return err
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
