package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoCallerNameRejectsInvalidLauncherPID(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		name, err := GoCallerName(pid)
		require.Empty(t, name)
		require.EqualError(t, err, "Go launcher process is unavailable")
	}
}
