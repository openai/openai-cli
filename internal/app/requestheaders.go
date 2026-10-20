package app

import (
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

// NewRequestHeaderFlag forwards the generated runtime bridge to request ownership.
// Remove this forwarding function when the authoritative generator updates its caller.
func NewRequestHeaderFlag() cli.Flag {
	return requestconfig.NewRequestHeaderFlag()
}
