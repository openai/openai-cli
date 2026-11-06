//go:build !windows

package skills

import (
	"errors"
	"os"
)

// Unlink before writing. The kernel releases the contents even if the CLI is interrupted.
func archiveTemp() (*os.File, error) {
	f, err := os.CreateTemp("", "openai-skill-*.zip")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
