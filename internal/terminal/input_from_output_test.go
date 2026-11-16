package terminal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenInputFromOutputRejectsMissingOrNonTerminalOutput(t *testing.T) {
	input, err := OpenInputFromOutput(nil)
	require.Error(t, err)
	require.Nil(t, input)
	output, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer output.Close()
	input, err = OpenInputFromOutput(output)
	require.Error(t, err)
	require.Nil(t, input)
	_, err = output.WriteString("still owned by caller")
	require.NoError(t, err)
}
