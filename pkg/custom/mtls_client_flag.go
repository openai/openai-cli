package custom

import (
	"errors"

	"github.com/urfave/cli/v3"
)

// mtlsClientFlag permits one explicit value, independently of an environment default.
// The embedded PostParse applies sources without consuming the explicit allowance.
type mtlsClientFlag struct {
	cli.StringFlag
	explicit bool
}

func (f *mtlsClientFlag) PreParse() error {
	f.explicit = false
	return f.StringFlag.PreParse()
}

func (f *mtlsClientFlag) Set(name, value string) error {
	if f.explicit {
		return errors.New("can't duplicate this flag")
	}
	f.explicit = true
	return f.StringFlag.Set(name, value)
}

// CLIStringFlag exposes metadata without changing parsing or source ownership.
func (f *mtlsClientFlag) CLIStringFlag() *cli.StringFlag { return &f.StringFlag }
