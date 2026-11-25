package request

import (
	"fmt"
	"strings"
)

// MissingRequiredFlagsError reports absent options after request input merges.
// Names contains declared flags, never request values.
type MissingRequiredFlagsError struct {
	Names []string
}

func (e *MissingRequiredFlagsError) Error() string {
	if len(e.Names) == 1 {
		return fmt.Sprintf("Required flag %q not set", e.Names[0])
	}
	return fmt.Sprintf("Required flags %q not set", strings.Join(e.Names, ", "))
}
