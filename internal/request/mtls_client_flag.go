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
	return f.StringFlag.PreParse()
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
