package requestflag

import (
	"fmt"

	"github.com/urfave/cli/v3"
)

type untypedFlag interface {
	cli.Flag
	isUntyped() bool
	setUntypedStdin(any) (bool, error)
	setStructured(any) error
	describeStructured(name string)
}

func (f *Flag[T]) setStructured(value any) error {
	if f.innerSet {
		return fmt.Errorf("set --%s-structured before nested --%s flags", f.Name, f.Name)
	}
	if f.IsSet() && !f.structured {
		return fmt.Errorf("cannot combine --%s with --%s-structured", f.Name, f.Name)
	}
	// Repeated untyped array flags previously appended each additional value.
	if f.structured {
		if items, ok := f.Get().([]any); ok {
			value = append(items, value)
		}
	}
	_, err := f.setUntypedStdin(value)
	if err == nil {
		f.structured = true
	}
	return err
}

func (f *InnerFlag[T]) setStructured(value any) error {
	if f.literal {
		return fmt.Errorf("cannot combine --%s with --%s-structured", f.Name, f.Name)
	}
	_, err := f.setUntypedStdin(value)
	if err == nil {
		f.structured = true
	}
	return err
}

func (f *Flag[T]) describeStructured(name string) {
	f.Usage = "Text is literal. For JSON or YAML, use --" + name + ".\n\n" + f.Usage
}

func (f *InnerFlag[T]) describeStructured(name string) {
	f.Usage = "Text is literal. For JSON or YAML, use --" + name + ".\n\n" + f.Usage
}

// structuredFlag explicitly selects YAML/JSON parsing for an untyped flag.
type structuredFlag struct {
	*Flag[string]
	target untypedFlag
}

func (f *structuredFlag) Set(name, raw string) error {
	var value any
	if err := UnmarshalYAMLOrJSON([]byte(raw), &value); err != nil {
		return fmt.Errorf("invalid structured value for --%s: %w", name, err)
	}
	if err := f.target.setStructured(value); err != nil {
		return err
	}
	return f.Flag.Set(name, raw)
}

// AddStructuredFlags adds an explicit structured-value alternative beside
// each untyped request flag, without modifying generated commands.
func AddStructuredFlags(root *cli.Command) {
	for _, command := range root.Commands {
		AddStructuredFlags(command)
	}
	existing := make(map[string]bool)
	for _, flag := range root.Flags {
		for _, name := range flag.Names() {
			existing[name] = true
		}
	}
	original := append([]cli.Flag(nil), root.Flags...)
	for _, flag := range original {
		target, ok := flag.(untypedFlag)
		if !ok || !target.isUntyped() || len(flag.Names()) == 0 {
			continue
		}
		name := flag.Names()[0] + "-structured"
		if existing[name] {
			continue
		}
		if input, ok := flag.(InRequest); ok && input.GetBodyPath() == "" && input.GetQueryPath() == "" && input.GetHeaderPath() == "" && input.GetPathParam() == "" && !input.IsBodyRoot() {
			continue
		}
		root.Flags = append(root.Flags, &structuredFlag{
			Flag:   &Flag[string]{Name: name, Usage: "Set --" + flag.Names()[0] + " from a JSON or YAML value."},
			target: target,
		})
		target.describeStructured(name)
		existing[name] = true
	}
}
