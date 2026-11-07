package requestflag

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/urfave/cli/v3"
)

// InputPolicy describes the schema of the value consumed by one flag occurrence.
// Generated declarations supply it; Go's any type cannot establish this policy.
type InputPolicy uint8

const (
	InputPolicyUnknown InputPolicy = iota
	InputPolicyTyped
	InputPolicyText
	InputPolicyTextStructured
	// The older command snapshot has no schema annotation for this operand.
	// Keep its original flag parser until the producer supplies a policy.
	inputPolicyExistingParser
)

const InputModeEnv = "OPENAI_CLI_INPUT_MODE"

// InputValue retains explicit structured input until request preparation resolves
// its source. Parsed strings must never be interpreted as another file source.
type InputValue struct {
	Raw      string
	Value    any
	Option   string
	Parse    func(string) (any, error)
	Previous *InputValue
	Combine  func(previous, current any) (any, error)
	// ResolveSources retains ordinary source handling for an explicitly supplied nested flag.
	ResolveSources bool
}

type inputPolicyFlag interface {
	cli.Flag
	inputPolicy() InputPolicy
	setInputPolicy(InputPolicy)
	setLegacyInput(bool)
}

func (f *Flag[T]) inputPolicy() InputPolicy          { return f.InputPolicy }
func (f *Flag[T]) setLegacyInput(legacy bool)        { f.legacyInput = legacy }
func (f *InnerFlag[T]) inputPolicy() InputPolicy     { return f.InputPolicy }
func (f *InnerFlag[T]) setLegacyInput(legacy bool)   { f.legacyInput = legacy }
func (f *Flag[T]) setInputPolicy(p InputPolicy)      { f.InputPolicy = p }
func (f *InnerFlag[T]) setInputPolicy(p InputPolicy) { f.InputPolicy = p }

// ConfigureUnannotatedCommands supports older generated command snapshots.
// Call it on canonical resources before adding handwritten commands or aliases.
// If the producer supplied any policies, it owns the entire snapshot and
// ConfigureInputMode will diagnose omissions instead of filling them in.
func ConfigureUnannotatedCommands(root *cli.Command, textUnionFields map[string][]string) {
	if hasDeclaredPolicy(root) {
		return
	}
	for _, resource := range root.Commands {
		for _, action := range resource.Commands {
			fields := textUnionFields[resource.Name+" "+action.Name]
			for _, flag := range action.Flags {
				operand, ok := flag.(inputPolicyFlag)
				if !ok || !isRequestOperand(flag) {
					continue
				}
				policy := inputPolicyExistingParser
				if len(fields) != 0 && slices.Contains(fields, operandBodyPath(flag)) {
					policy = InputPolicyTextStructured
				}
				operand.setInputPolicy(policy)
			}
		}
	}
}

func hasDeclaredPolicy(command *cli.Command) bool {
	for _, flag := range command.Flags {
		if operand, ok := flag.(inputPolicyFlag); ok && operand.inputPolicy() != InputPolicyUnknown {
			return true
		}
	}
	return slices.ContainsFunc(command.Commands, hasDeclaredPolicy)
}

func operandBodyPath(flag cli.Flag) string {
	if inner, ok := flag.(HasOuterFlag); ok {
		return operandBodyPath(inner.GetOuterFlag()) + "." + inner.GetInnerField()
	}
	if field, ok := flag.(InRequest); ok {
		return field.GetBodyPath()
	}
	return ""
}

// ConfigureInputMode captures one process's policy before argument parsing.
// It never changes the environment or shares mutable process-global state.
func ConfigureInputMode(root *cli.Command, mode string) error {
	if mode != "" && mode != "literal" && mode != "legacy" {
		return &InputPolicyError{message: "invalid " + InputModeEnv + ": use literal or legacy"}
	}
	seen := map[cli.Flag]bool{}
	var visit func(*cli.Command) error
	visit = func(command *cli.Command) error {
		names := map[string][]cli.Flag{}
		for _, flag := range command.Flags {
			for _, name := range flag.Names() {
				names[name] = append(names[name], flag)
			}
		}
		for _, flag := range command.Flags {
			if target, ok := flag.(structuredTarget); ok && target.inputPolicy() == InputPolicyTextStructured && isRequestOperand(flag) {
				for _, name := range flag.Names() {
					for _, occupied := range names[name+"-structured"] {
						companion, ok := occupied.(*structuredFlag)
						if !ok || companion.target != target {
							return &InputPolicyError{message: "input policy companion collision for --" + name + "-structured"}
						}
					}
				}
			}

			policy, ok := flag.(inputPolicyFlag)
			if !ok || seen[flag] {
				continue
			}
			seen[flag] = true
			if policy.inputPolicy() == InputPolicyUnknown && isRequestOperand(flag) {
				return &InputPolicyError{message: "missing input policy for --" + flag.Names()[0]}
			}
			policy.setLegacyInput(mode == "legacy")
		}
		for _, child := range command.Commands {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(root)
}

func isRequestOperand(flag cli.Flag) bool {
	if _, ok := flag.(HasOuterFlag); ok {
		return true
	}
	if f, ok := flag.(InRequest); ok {
		return f.GetBodyPath() != "" || f.GetQueryPath() != "" || f.GetHeaderPath() != "" || f.GetPathParam() != "" || f.IsBodyRoot()
	}
	return false
}

func (p InputPolicy) preservesText() bool {
	return p == InputPolicyText || p == InputPolicyTextStructured
}

// literalArgument constructs the string representation required by the adapter.
func literalArgument[T any](raw string) (T, error) {
	var zero T
	typ := reflect.TypeFor[T]()
	value := reflect.ValueOf(raw)
	if typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.String {
		ptr := reflect.New(typ.Elem())
		ptr.Elem().SetString(raw)
		return ptr.Interface().(T), nil
	}
	if value.Type().AssignableTo(typ) {
		return any(raw).(T), nil
	}
	return zero, fmt.Errorf("text input policy does not match flag adapter %s", typ)
}

func unwrappedInput(value any) any {
	if input, ok := value.(InputValue); ok && input.Parse == nil && input.Previous == nil {
		return input.Value
	}
	return value
}

func (f *Flag[T]) setLiteralValue(raw string) error {
	typ := reflect.TypeFor[T]()
	if typ.Kind() == reflect.Slice {
		current := reflect.ValueOf(f.value.Get())
		item := reflect.ValueOf(raw)
		if !item.Type().AssignableTo(typ.Elem()) {
			return fmt.Errorf("text input policy does not match flag adapter %s", typ)
		}
		if !current.IsValid() {
			current = reflect.MakeSlice(typ, 0, 1)
		}
		f.value = &cliValue[T]{reflect.Append(current, item).Interface().(T)}
		return nil
	}
	value, err := literalArgument[T](raw)
	if err == nil {
		f.value = &cliValue[T]{value}
	}
	return err
}

// InputPolicyError contains only policy names and declared flag names.
type InputPolicyError struct{ message string }

func (e *InputPolicyError) Error() string { return e.message }

func acceptsParsedInput[T any](policy InputPolicy) bool {
	typ := reflect.TypeFor[T]()
	return typ.Kind() == reflect.Interface || (policy.preservesText() && typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.String)
}
