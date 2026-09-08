//go:build !windows

package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestJSONColorEnvironmentPagerBackends(t *testing.T) {
	for name, stream := range map[string]func(string, func(*os.File) error) error{
		"pipe": streamToPagerWithPipe, "socket": streamOutputOSSpecific,
	} {
		for _, format := range []string{"json", "jsonl"} {
			for _, noColor := range []string{"unset", "", "1", "0"} {
				for _, force := range []string{"unset", "", "0", "1", "auto"} {
					t.Run(name+"/"+format+"/no="+noColor+"/force="+force, func(t *testing.T) {
						outputPath := configureCapturePager(t)
						setColorEnvironment(t, "NO_COLOR", noColor)
						setColorEnvironment(t, "FORCE_COLOR", force)
						// The first item is formatted before the pager starts, the next after.
						res := gjson.Parse(`{"message":"safe"}`)
						opts := ShowJSONOpts{Format: format, Stdout: os.Stdout}
						first, err := formatJSON(res, opts)
						require.NoError(t, err)
						require.NoError(t, stream("test", func(w *os.File) error {
							if name == "socket" {
								require.Equal(t, "parent-socket", w.Name(), "must exercise the socket backend")
							}
							if _, err := w.Write(first); err != nil {
								return err
							}
							next, err := formatJSONForOutput(res, ShowJSONOpts{Format: format, Stdout: w}, os.Stdout)
							if err != nil {
								return err
							}
							_, err = w.Write(next)
							return err
						}))
						output, err := os.ReadFile(outputPath)
						require.NoError(t, err)
						require.Equal(t, string(first)+string(first), string(output))
						if noColor == "1" || noColor == "0" {
							value, exists := os.LookupEnv("FORCE_COLOR")
							require.Equal(t, force != "unset", exists)
							if exists {
								require.Equal(t, force, value, "pager must preserve the user's color choice")
							}
						}
					})
				}
			}
		}
	}
}
