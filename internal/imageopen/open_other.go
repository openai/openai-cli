//go:build !darwin && !linux && !windows

package imageopen

import (
	"context"
	"errors"
)

func launch(context.Context, string) error {
	return errors.New("opening images in a viewer is not supported on this system")
}
