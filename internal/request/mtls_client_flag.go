package request

import (
	"errors"

	"github.com/urfave/cli/v3"
)

// MTLSClientFlag permits one explicit value, independently of an environment default.
// The embedded PostParse applies sources without consuming the explicit allowance.
type MTLSClientFlag struct {
	cli.StringFlag
	explicit bool
}

func (f *MTLSClientFlag) PreParse() error {
	f.explicit = false
	// urfave retains source presence and duplicate counts across invocations.
	// Preserve the current public definition and the metadata pointer in place.
	flag := &f.StringFlag
	*flag = cli.StringFlag{
		Name: flag.Name, Category: flag.Category, DefaultText: flag.DefaultText,
		HideDefault: flag.HideDefault, Usage: flag.Usage, Sources: flag.Sources,
		Required: flag.Required, Hidden: flag.Hidden, Deprecated: flag.Deprecated,
		Local: flag.Local, Value: flag.Value, Destination: flag.Destination,
		Aliases: flag.Aliases, TakesFile: flag.TakesFile, Action: flag.Action,
		Config: flag.Config, OnlyOnce: flag.OnlyOnce, Validator: flag.Validator,
		ValidateDefaults: flag.ValidateDefaults,
	}
	return flag.PreParse()
}

func (f *MTLSClientFlag) Set(name, value string) error {
	if f.explicit {
		return errors.New("can't duplicate this flag")
	}
	f.explicit = true
	return f.StringFlag.Set(name, value)
}

// CLIStringFlag exposes metadata without changing parsing or source ownership.
func (f *MTLSClientFlag) CLIStringFlag() *cli.StringFlag { return &f.StringFlag }
