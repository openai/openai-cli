package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShowJSONIteratorExploreFormatCase(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	for _, format := range []string{"explore", "EXPLORE", "ExPlOrE"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%t", format, explicit), func(t *testing.T) {
				stdout, err := os.CreateTemp(t.TempDir(), "stdout")
				require.NoError(t, err)
				defer stdout.Close()
				var stderr bytes.Buffer
				iter := &sliceIterator[map[string]any]{items: []map[string]any{
					{"id": "first"},
					{"id": "not-visited"},
				}}

				err = ShowJSONIterator(iter, 1, ShowJSONOpts{
					Format:         format,
					ExplicitFormat: explicit,
					Stdout:         stdout,
					Stderr:         &stderr,
					Transform:      "id",
				})
				require.NoError(t, err)
				output, err := os.ReadFile(stdout.Name())
				require.NoError(t, err)
				require.Equal(t, "\"first\"\n", string(output))
				require.Equal(t, 1, iter.index, "must not consume the next item")
				if explicit {
					require.Equal(t, warningExploreNotSupported, stderr.String())
				} else {
					require.Empty(t, stderr.String())
				}
			})
		}
	}
}

func TestShowJSONIteratorExploreFormatCaseTTY(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("explorer dispatch regression requires a terminal stdout")
	}
	for _, format := range []string{"explore", "EXPLORE", "ExPlOrE"} {
		t.Run(format, func(t *testing.T) {
			// An iterator error stops explorer preloading before the interactive UI starts.
			upstream := errors.New("synthetic iterator failure")
			iter := &failingOutputIterator{
				sliceIterator: sliceIterator[string]{items: []string{"first"}},
				err:           upstream,
			}
			var stderr bytes.Buffer
			err := ShowJSONIterator[string](iter, -1, ShowJSONOpts{
				Format:         format,
				ExplicitFormat: true,
				Stdout:         os.Stdout,
				Stderr:         &stderr,
			})
			require.ErrorIs(t, err, upstream)
			require.Equal(t, 2, iter.index)
			require.Empty(t, stderr.String())
		})
	}
}
