package requestflag

import (
	"reflect"
	"strings"
)

// InputValue holds a flag's validated value and any occurrences that still need
// a file or stdin. Replaced scalar operands never enter the pending sequence.
type InputValue struct {
	option        string
	value         any
	expandSources bool // an ordinary nested flag inside a structured parent
	pending       []structuredOperand
	target        reflect.Type
	repeat        bool
	validate      func(reflect.Value) error
}

type structuredOperand struct {
	parsed any
	source string // nonempty only for an explicit @ or escaped \@ operand
}

func structuredFlagInput[T any](raw, option string, validator func(T) error, repeat bool, previous *InputValue) (InputValue, error) {
	input := InputValue{option: option, target: reflect.TypeFor[T](), repeat: repeat}
	if validator != nil {
		input.validate = func(v reflect.Value) error { return validator(inputTyped[T](v)) }
	}
	if previous != nil {
		input.value, input.pending = previous.value, previous.pending
	}
	var operand structuredOperand
	if strings.HasPrefix(raw, "@") || strings.HasPrefix(raw, "\\@") {
		operand.source = raw
	} else {
		value, err := decodeStructuredOperand(raw, input.target, option)
		if err != nil {
			return InputValue{}, err
		}
		operand.parsed = value
	}
	deferred := len(input.pending) != 0 || operand.source != ""
	if deferred {
		// A later Resolve may append to these arrays. Never share writable
		// capacity with a stored operand or between evaluations.
		if len(input.pending) == 0 {
			input.value = clipInputSlice(input.value)
		}
		operand.parsed = clipInputSlice(operand.parsed)
	}
	input.pending = append(input.pending, operand)
	if deferred {
		return input, nil
	}
	// Fold a fully known value once, including validation. Later occurrences
	// start at this snapshot rather than reapplying its validator.
	value, err := input.Resolve(nil, nil)
	return InputValue{value: value, option: option}, err
}

// Option names the flag responsible for a source error.
func (input InputValue) Option() string {
	if input.option == "" {
		return ""
	}
	return "--" + input.option
}

// Resolve evaluates effective operands in command-line order. readText expands
// explicit structured sources as text. prepare walks values to resolve nested
// flags; strings in decoded data stay protected. A nil prepare leaves values
// as parsed. readText is required only when a source is present.
func (input InputValue) Resolve(readText func(string) (string, error), prepare func(any, bool) (any, error)) (any, error) {
	result := input.value
	var err error
	if prepare != nil {
		result, err = prepare(result, input.expandSources)
		if err != nil {
			return nil, err
		}
	}
	for _, operand := range input.pending {
		current := operand.parsed
		if operand.source != "" {
			if readText == nil {
				return nil, structuredInputError(input.option, "input source needs a resolver")
			}
			text, err := readText(operand.source)
			if err != nil {
				return nil, err
			}
			current, err = decodeStructuredOperand(text, input.target, input.option)
			if err != nil {
				return nil, err
			}
		}
		if prepare != nil {
			current, err = prepare(current, false)
			if err != nil {
				return nil, err
			}
		}
		result, err = input.combine(result, current)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func clipInputSlice(value any) any {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Slice {
		return v.Slice3(0, v.Len(), v.Len()).Interface()
	}
	return value
}

func (input InputValue) combine(previous, current any) (any, error) {
	result := current
	if input.repeat && (input.target.Kind() == reflect.Slice || reflect.ValueOf(previous).Kind() == reflect.Slice) {
		typ := input.target
		if typ.Kind() == reflect.Interface {
			typ = reflect.TypeFor[[]any]()
		}
		items, err := inputAsType(previous, typ)
		if err != nil {
			return nil, structuredInputError(input.option, "incompatible value type")
		}
		item, err := inputAsType(current, typ.Elem())
		if err != nil {
			return nil, structuredInputError(input.option, "incompatible value type")
		}
		result = reflect.Append(items, item).Interface()
	}
	parsed, err := inputAsType(result, input.target)
	if err != nil {
		return nil, structuredInputError(input.option, "incompatible value type")
	}
	if input.validate != nil && input.validate(parsed) != nil {
		return nil, structuredInputError(input.option, "value failed validation")
	}
	return parsed.Interface(), nil
}

// ProtectStdin marks the materialized tree, including nested flag values, with
// the request's stdin provenance. Deferred sources came from explicit CLI flags.
func (input InputValue) ProtectStdin(protect func(any) any) InputValue {
	input.value = protect(input.value)
	return input
}

// CountSources visits the effective values without decoding or reading any
// source. Only explicitly supplied sources and ordinary nested flags can
// consume stdin; decoded containers can still contain such nested flags.
func (input InputValue) CountSources(count func(value any, expandSources bool) int) int {
	total := count(input.value, input.expandSources)
	for _, operand := range input.pending {
		if operand.source != "" {
			total += count(operand.source, true)
		} else {
			total += count(operand.parsed, false)
		}
	}
	return total
}
